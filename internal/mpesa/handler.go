// Package mpesa implements the HTTP handler and processing pipeline
// for Safaricom Daraja C2B callbacks.
//
// It coordinates validation, unit matching, payment recording,
// and outbound notifications using injected dependencies.
package mpesa

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// MatcherService resolves an account reference to a unit.
type MatcherService interface {
	Match(ctx context.Context, landlordID, accountRef string) (*models.Unit, error)
}

// LedgerService records a payment and returns its status.
type LedgerService interface {
	Record(ctx context.Context, payment models.Payment) (*models.Payment, error)
}

// NotifierService sends outbound messages.
type NotifierService interface {
	NotifyLandlord(ctx context.Context, landlordPhone string, payment *models.Payment, unit *models.Unit) error
	NotifyTenant(ctx context.Context, phone string, payment *models.Payment, unit *models.Unit) error
}

// LandlordRepository fetches landlord rows by paybill number.
type LandlordRepository interface {
	GetByPaybill(ctx context.Context, paybill string) (*models.Landlord, error)
}

// Handler handles inbound Daraja C2B callbacks.
type Handler struct {
	matcher   MatcherService
	ledger    LedgerService
	notifier  NotifierService
	landlords LandlordRepository
	isDev     bool
}

// NewHandler wires all dependencies. Called from main.go.
func NewHandler(
	matcher MatcherService,
	ledger LedgerService,
	notifier NotifierService,
	landlords LandlordRepository,
	isDev bool,
) *Handler {
	return &Handler{
		matcher:   matcher,
		ledger:    ledger,
		notifier:  notifier,
		landlords: landlords,
		isDev:     isDev,
	}
}

// Callback handles POST /mpesa/c2b/callback.
//
// Design constraints from Daraja:
//   - Must return HTTP 200 within 5 seconds or Safaricom retries.
//   - All heavy work runs in a goroutine after the response is sent.
//   - The transaction_id UNIQUE constraint in Postgres is the
//     idempotency guard against duplicate callbacks.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	if err := ValidateIP(r, h.isDev); err != nil {
		slog.Warn("mpesa callback: blocked IP", "error", err, "remote", r.RemoteAddr)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var cb C2BCallback
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		slog.Warn("mpesa callback: malformed JSON", "error", err)
		writeJSON(w, http.StatusBadRequest, C2BResponse{
			ResultCode: "1",
			ResultDesc: "Bad Request",
		})
		return
	}

	if err := ValidatePayload(&cb); err != nil {
		slog.Warn("mpesa callback: invalid payload", "error", err, "trans_id", cb.TransID)
		writeJSON(w, http.StatusUnprocessableEntity, C2BResponse{
			ResultCode: "1",
			ResultDesc: "Invalid payload",
		})
		return
	}

	slog.Info("mpesa callback: received",
		"trans_id", cb.TransID,
		"amount", cb.TransAmount,
		"ref", cb.BillRefNumber,
		"msisdn", cb.MSISDN,
	)

	// Respond to Safaricom immediately — must be within 5 seconds.
	// All processing happens asynchronously in the goroutine below.
	writeJSON(w, http.StatusOK, successResponse)

	go h.process(cb)
}

// process runs the full payment pipeline after the HTTP response is sent.
// All errors are logged — nothing panics the server.
func (h *Handler) process(cb C2BCallback) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := slog.With("trans_id", cb.TransID, "ref", cb.BillRefNumber)

	// ── Resolve landlord from paybill ─────────────────────────────────────────
	landlord, err := h.landlords.GetByPaybill(ctx, cb.BusinessShortCode)
	if err != nil {
		log.Error("process: landlord not found", "paybill", cb.BusinessShortCode, "error", err)
		return
	}

	// ── Match account reference to a unit ─────────────────────────────────────
	unit, err := h.matcher.Match(ctx, landlord.ID, cb.BillRefNumber)
	if err != nil {
		log.Warn("process: no matching unit",
			"ref", cb.BillRefNumber,
			"landlord_id", landlord.ID,
			"error", err,
		)
		h.handleUnmatched(ctx, cb, landlord)
		return
	}

	// ── Parse amount ──────────────────────────────────────────────────────────
	amount, err := ParseAmount(cb.TransAmount)
	if err != nil {
		log.Error("process: parse amount failed", "error", err)
		return
	}

	// ── Build payment record ──────────────────────────────────────────────────
	payment := models.Payment{
		TransactionID: cb.TransID,
		UnitID:        unit.ID,
		LandlordID:    landlord.ID,
		TenantPhone:   cb.MSISDN,
		Amount:        amount,
		MonthKey:      monthKey(cb.TransTime),
		PaidAt:        time.Now(),
	}

	// ── Record in ledger (idempotent via UNIQUE transaction_id) ───────────────
	recorded, err := h.ledger.Record(ctx, payment)
	if err != nil {
		log.Error("process: ledger record failed", "error", err)
		return
	}

	// nil recorded means the transaction_id already exists — duplicate callback.
	// The UNIQUE constraint silently discarded it. Nothing more to do.
	if recorded == nil {
		log.Info("process: duplicate transaction skipped", "trans_id", cb.TransID)
		return
	}

	log.Info("process: payment recorded",
		"payment_id", recorded.ID,
		"unit", unit.UnitRef,
		"amount", recorded.Amount,
		"status", recorded.Status,
	)

	// ── Notify landlord ───────────────────────────────────────────────────────
	// Non-fatal: payment is already recorded safely. A notification failure
	// must never prevent the tenant receipt from being sent.
	if err := h.notifier.NotifyLandlord(ctx, landlord.WhatsAppPhone, recorded, unit); err != nil {
		log.Error("process: landlord notification failed", "error", err)
	}

	// ── Send receipt to tenant via SMS ────────────────────────────────────────
	if err := h.notifier.NotifyTenant(ctx, cb.MSISDN, recorded, unit); err != nil {
		log.Error("process: tenant receipt failed", "error", err)
	}
}

// handleUnmatched records a payment that could not be matched to a unit
// and alerts the landlord so they can claim it manually via CLAIM command.
func (h *Handler) handleUnmatched(ctx context.Context, cb C2BCallback, landlord *models.Landlord) {
	log := slog.With("trans_id", cb.TransID, "ref", cb.BillRefNumber)

	amount, err := ParseAmount(cb.TransAmount)
	if err != nil {
		log.Error("handleUnmatched: parse amount failed", "error", err)
		return
	}

	payment := models.Payment{
		TransactionID: cb.TransID,
		LandlordID:    landlord.ID,
		TenantPhone:   cb.MSISDN,
		Amount:        amount,
		MonthKey:      monthKey(cb.TransTime),
		Status:        "unmatched",
		PaidAt:        time.Now(),
	}

	recorded, err := h.ledger.Record(ctx, payment)
	if err != nil {
		log.Error("handleUnmatched: ledger record failed", "error", err)
		return
	}

	// nil = duplicate — already handled, nothing to do
	if recorded == nil {
		return
	}

	log.Warn("handleUnmatched: payment recorded as unmatched",
		"payment_id", recorded.ID,
		"ref", cb.BillRefNumber,
	)

	if err := h.notifier.NotifyLandlord(ctx, landlord.WhatsAppPhone, recorded, nil); err != nil {
		log.Error("handleUnmatched: landlord notification failed", "error", err)
	}
}

// monthKey derives a YYYY-MM string from Safaricom's YYYYMMDDHHmmss format.
func monthKey(transTime string) string {
	if len(transTime) < 6 {
		return time.Now().Format("2006-01")
	}
	return transTime[:4] + "-" + transTime[4:6]
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode failed", "error", err)
	}
}
