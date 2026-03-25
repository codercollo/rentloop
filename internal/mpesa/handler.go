// Package mpesa implements the HTTP handler and processing pipeline
// for Safaricom Daraja C2B callbacks.
package mpesa

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/codercollo/rentloop/internal/billing"
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
	NotifyLandlord(ctx context.Context, landlordPhone string, payment *models.Payment, unit *models.Unit, apartmentName string) error
	NotifyTenant(ctx context.Context, phone string, payment *models.Payment, unit *models.Unit, apartmentName string) error
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
	billing   *billing.Handler // nil-safe: routes RENTLOOP- refs to billing service
	isDev     bool
}

// NewHandler wires all dependencies. billing may be nil in tests.
func NewHandler(
	matcher MatcherService,
	ledger LedgerService,
	notifier NotifierService,
	landlords LandlordRepository,
	billing *billing.Handler,
	isDev bool,
) *Handler {
	return &Handler{
		matcher:   matcher,
		ledger:    ledger,
		notifier:  notifier,
		landlords: landlords,
		billing:   billing,
		isDev:     isDev,
	}
}

// Callback handles POST /mpesa/c2b/callback.
//
// Design constraints:
//   - Must return HTTP 200 within 5 seconds or Safaricom retries.
//   - All processing is async — response is sent before work starts.
//   - transaction_id UNIQUE constraint guards against duplicate callbacks.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	if err := ValidateIP(r, h.isDev); err != nil {
		slog.Warn("mpesa callback: blocked IP", "error", err, "remote", r.RemoteAddr)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var cb C2BCallback
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		slog.Warn("mpesa callback: malformed JSON", "error", err)
		writeJSON(w, http.StatusBadRequest, C2BResponse{ResultCode: "1", ResultDesc: "Bad Request"})
		return
	}

	if err := ValidatePayload(&cb); err != nil {
		slog.Warn("mpesa callback: invalid payload", "error", err, "trans_id", cb.TransID)
		writeJSON(w, http.StatusUnprocessableEntity, C2BResponse{ResultCode: "1", ResultDesc: "Invalid payload"})
		return
	}

	slog.Info("mpesa callback: received",
		"trans_id", cb.TransID,
		"amount", cb.TransAmount,
		"ref", cb.BillRefNumber,
		"msisdn", cb.MSISDN,
	)

	writeJSON(w, http.StatusOK, successResponse)
	go h.process(cb)
}

// process runs the full pipeline after the HTTP response is sent.
func (h *Handler) process(cb C2BCallback) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := slog.With("trans_id", cb.TransID, "ref", cb.BillRefNumber)

	// ── Subscription payment — route to billing, skip rent ledger ─────────────
	// RENTLOOP-{id} refs are landlord subscription payments.
	// They must never enter the rent ledger.
	if billing.IsSubscriptionPayment(cb.BillRefNumber) {
		log.Info("process: subscription payment", "ref", cb.BillRefNumber)
		amount, err := ParseAmount(cb.TransAmount)
		if err != nil {
			log.Error("process: subscription parse amount failed", "error", err)
			return
		}
		if h.billing != nil {
			h.billing.ProcessSubscription(cb.TransID, cb.BillRefNumber, amount)
		}
		return
	}

	// ── Resolve landlord from paybill ─────────────────────────────────────────
	landlord, err := h.landlords.GetByPaybill(ctx, cb.BusinessShortCode)
	if err != nil {
		log.Error("process: landlord not found", "paybill", cb.BusinessShortCode, "error", err)
		return
	}

	// ── Match account reference to a unit ─────────────────────────────────────
	unit, err := h.matcher.Match(ctx, landlord.ID, cb.BillRefNumber)
	if err != nil {
		log.Warn("process: no matching unit", "landlord_id", landlord.ID, "error", err)
		h.handleUnmatched(ctx, cb, landlord)
		return
	}

	// ── Parse amount ──────────────────────────────────────────────────────────
	amount, err := ParseAmount(cb.TransAmount)
	if err != nil {
		log.Error("process: parse amount failed", "error", err)
		return
	}

	// ── Record in ledger ──────────────────────────────────────────────────────
	payment := models.Payment{
		TransactionID: cb.TransID,
		UnitID:        unit.ID,
		LandlordID:    landlord.ID,
		TenantPhone:   cb.MSISDN,
		Amount:        amount,
		MonthKey:      monthKey(cb.TransTime),
		PaidAt:        time.Now(),
	}

	recorded, err := h.ledger.Record(ctx, payment)
	if err != nil {
		log.Error("process: ledger record failed", "error", err)
		return
	}

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

	// ── Notify landlord + tenant ──────────────────────────────────────────────
	if err := h.notifier.NotifyLandlord(ctx, landlord.WhatsAppPhone, recorded, unit, landlord.ApartmentName); err != nil {
		log.Error("process: landlord notification failed", "error", err)
	}
	if err := h.notifier.NotifyTenant(ctx, cb.MSISDN, recorded, unit, landlord.ApartmentName); err != nil {
		log.Error("process: tenant receipt failed", "error", err)
	}
}

// handleUnmatched records an unrecognised payment and alerts the landlord.
func (h *Handler) handleUnmatched(ctx context.Context, cb C2BCallback, landlord *models.Landlord) {
	log := slog.With("trans_id", cb.TransID, "ref", cb.BillRefNumber)

	amount, err := ParseAmount(cb.TransAmount)
	if err != nil {
		log.Error("handleUnmatched: parse amount failed", "error", err)
		return
	}

	recorded, err := h.ledger.Record(ctx, models.Payment{
		TransactionID: cb.TransID,
		LandlordID:    landlord.ID,
		TenantPhone:   cb.MSISDN,
		Amount:        amount,
		MonthKey:      monthKey(cb.TransTime),
		Status:        "unmatched",
		PaidAt:        time.Now(),
	})
	if err != nil {
		log.Error("handleUnmatched: ledger record failed", "error", err)
		return
	}
	if recorded == nil {
		return
	}

	log.Warn("handleUnmatched: recorded", "payment_id", recorded.ID)

	if err := h.notifier.NotifyLandlord(ctx, landlord.WhatsAppPhone, recorded, nil, landlord.ApartmentName); err != nil {
		log.Error("handleUnmatched: notify failed", "error", err)
	}
}

// monthKey derives YYYY-MM from Safaricom's YYYYMMDDHHmmss format.
func monthKey(transTime string) string {
	if len(transTime) < 6 {
		return time.Now().Format("2006-01")
	}
	return transTime[:4] + "-" + transTime[4:6]
}

// writeJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode failed", "error", err)
	}
}
