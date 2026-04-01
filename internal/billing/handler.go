package billing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Handler routes subscription payments to the billing service.
type Handler struct {
	svc *Service
	stk STKPusher // injected via SetSTKClient
}

// STKPusher is satisfied by stk.Client.
type STKPusher interface {
	Push(ctx context.Context, phone string, amount int, accountRef string) error
}

// NewHandler wires the service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SetSTKClient injects the STK client after construction.
// Call this in main.go after NewHandler.
func (h *Handler) SetSTKClient(c STKPusher) {
	h.stk = c
}

// TriggerSTK handles POST /billing/stk.
// Body: {"landlord_id": "...", "phone": "254712345678"}
func (h *Handler) TriggerSTK(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LandlordID string `json:"landlord_id"`
		Phone      string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.LandlordID == "" || req.Phone == "" {
		http.Error(w, "landlord_id and phone required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	landlord, err := h.svc.repo.GetLandlordByID(ctx, req.LandlordID)
	if err != nil {
		http.Error(w, "landlord not found", http.StatusNotFound)
		return
	}

	amount := h.svc.MonthlyAmount(landlord.UnitCount)
	if amount == 0 {
		// Free tier — nothing to charge.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Reference format matches IsSubscriptionPayment() prefix check.
	ref := SubscriptionRef(landlord.ID)

	if h.stk == nil {
		http.Error(w, "STK client not configured", http.StatusInternalServerError)
		return
	}

	if err := h.stk.Push(ctx, req.Phone, amount, ref); err != nil {
		slog.Error("billing: stk push failed",
			"landlord_id", req.LandlordID, "error", err)
		http.Error(w, "stk push failed", http.StatusBadGateway)
		return
	}

	slog.Info("billing: stk push initiated",
		"landlord_id", req.LandlordID,
		"amount", amount,
		"ref", ref,
	)

	w.WriteHeader(http.StatusOK)
}

// ProcessSubscriptionFromSTK is called by the STK callback handler.
// It reuses the same ProcessPayment path as C2B paybill payments.
func (h *Handler) ProcessSubscriptionFromSTK(receipt string, amount int) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// We don't have the landlord ref from the STK callback metadata directly,
		// but the receipt IS the transaction_id — pass it with the RENTLOOP- prefix
		// if you store the CheckoutRequestID→ref mapping, or look up by receipt.
		// Simplest safe approach: look up by receipt (transaction_id).
		if err := h.svc.ProcessPaymentByReceipt(ctx, receipt, amount); err != nil {
			slog.Error("billing: process stk subscription",
				"receipt", receipt, "error", err)
		}
	}()
}

// ProcessSubscription is called by the mpesa handler (not HTTP)
// when the account ref starts with RENTLOOP-.
func (h *Handler) ProcessSubscription(transactionID, ref string, amount int) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.svc.ProcessPayment(ctx, transactionID, ref, amount); err != nil {
			slog.Error("billing: process subscription",
				"transaction_id", transactionID,
				"error", err,
			)
		}
	}()
}

// Health is a no-op used to confirm the billing handler is wired.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
