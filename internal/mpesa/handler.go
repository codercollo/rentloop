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
	"github.com/codercollo/rentloop/internal/receipt"
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
	matcher    MatcherService
	ledger     LedgerService
	notifier   NotifierService
	landlords  LandlordRepository
	billing    *billing.Handler
	receiptSvc *receipt.Service
	isDev      bool
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

// SetReceiptService setter
func (h *Handler) SetReceiptService(svc *receipt.Service) {
	h.receiptSvc = svc
}

// Callback handles POST /mpesa/c2b/callback.
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
		writeJSON(w, http.StatusUnprocessableEntity, C2BResponse{
			ResultCode: "1",
			ResultDesc: "Invalid payload",
		})
		return
	}

	source := DetectPaymentSource(cb)

	slog.Info("mpesa callback: received",
		"trans_id", cb.TransID,
		"amount", cb.TransAmount,
		"ref", cb.BillRefNumber,
		"msisdn", cb.MSISDN,
		"source", source,
		"third_party_id", cb.ThirdPartyTransID,
	)

	writeJSON(w, http.StatusOK, successResponse)

	// ALL heavy work happens async
	go h.process(cb, source)
}

// process runs the full pipeline after the HTTP response is sent.
func (h *Handler) process(cb C2BCallback, source models.PaymentSource) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := slog.With(
		"trans_id", cb.TransID,
		"ref", cb.BillRefNumber,
		"source", source,
	)

	// ── Subscription payment ──────────────────────────────────────────────────
	if billing.IsSubscriptionPayment(cb.BillRefNumber) {
		log.Info("process: subscription payment")
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

	// ── Resolve landlord ──────────────────────────────────────────────────────
	landlord, err := h.landlords.GetByPaybill(ctx, cb.BusinessShortCode)
	if err != nil {
		log.Error("process: landlord not found",
			"paybill", cb.BusinessShortCode, "error", err)
		return
	}

	// use premise_name (fully-qualified) for notifications, falling
	// back to apartment_name. Previously landlord.ApartmentName was passed
	// directly, which gave "Sunrise Apartments" instead of
	// "Sunrise Apartments - Kitengela" — inconsistent with all bot responses.
	notifyName := landlord.PremiseName
	if notifyName == "" {
		notifyName = landlord.ApartmentName
	}

	// ── Match account reference to a unit ────────────────────────────────────
	unit, err := h.matcher.Match(ctx, landlord.ID, cb.BillRefNumber)
	if err != nil {
		log.Warn("process: no matching unit",
			"landlord_id", landlord.ID, "error", err)
		h.handleUnmatched(ctx, cb, landlord, notifyName, source)
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
		PaymentSource: source,
	}

	recorded, err := h.ledger.Record(ctx, payment)
	if err != nil {
		log.Error("process: ledger record failed", "error", err)
		return
	}

	if recorded == nil {
		log.Info("process: duplicate transaction skipped")
		return
	}

	log.Info("process: payment recorded",
		"payment_id", recorded.ID,
		"unit", unit.UnitRef,
		"amount", recorded.Amount,
		"status", recorded.Status,
		"source", recorded.PaymentSource,
	)

	// ── Notify landlord + tenant ──────────────────────────────────────────────
	// FIX 4: pass notifyName (premise_name with fallback) to both calls.
	if err := h.notifier.NotifyLandlord(ctx, landlord.WhatsAppPhone, recorded, unit, notifyName); err != nil {
		log.Error("process: landlord notification failed", "error", err)
	}

	if cb.MSISDN != "" && source != models.PaymentSourceBankPaybill {
		if err := h.notifier.NotifyTenant(ctx, cb.MSISDN, recorded, unit, notifyName); err != nil {
			log.Error("process: tenant receipt failed", "error", err)
		}
	}

	// ── Issue receipt (async) ────────────────────────────────────────────────
	if h.receiptSvc != nil {
		aptName := ""
		if landlord != nil {
			aptName = landlord.PremiseName
			if aptName == "" {
				aptName = landlord.ApartmentName
			}
		}

		h.receiptSvc.IssueAsync(receipt.IssueInput{
			Payment:       recorded,
			Unit:          unit,
			Landlord:      landlord,
			ApartmentName: aptName,
		}, func(result *receipt.IssueResult, err error) {
			if err != nil {
				slog.Error("receipt: issue failed",
					"payment_id", recorded.ID, "error", err)
				return
			}
			slog.Info("receipt: issued",
				"receipt_number", result.Receipt.ReceiptNumber,
				"url", result.PublicURL,
			)
		})
	}
}

// handleUnmatched records an unrecognised payment and alerts the landlord.
func (h *Handler) handleUnmatched(
	ctx context.Context,
	cb C2BCallback,
	landlord *models.Landlord,
	notifyName string,
	source models.PaymentSource,
) {
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
		Status:        models.PaymentStatusUnknown,
		PaymentSource: source,
		PaidAt:        time.Now(),
	})
	if err != nil {
		log.Error("handleUnmatched: ledger record failed", "error", err)
		return
	}
	if recorded == nil {
		return
	}

	log.Warn("handleUnmatched: recorded",
		"payment_id", recorded.ID,
		"source", source,
	)

	// FIX 4: use notifyName here too.
	if err := h.notifier.NotifyLandlord(ctx, landlord.WhatsAppPhone, recorded, nil, notifyName); err != nil {
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode failed", "error", err)
	}
}
