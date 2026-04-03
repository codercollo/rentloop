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
	stk STKPusher
}

// STKPusher is satisfied by stk.Client.
type STKPusher interface {
	Push(ctx context.Context, phone string, amount int, accountRef string) (string, error)
}

// NewHandler wires the service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SetSTKClient injects the STK client after construction.
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
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ref := SubscriptionRef(landlord.ID)

	if h.stk == nil {
		http.Error(w, "STK client not configured", http.StatusInternalServerError)
		return
	}

	checkoutID, err := h.stk.Push(ctx, req.Phone, amount, ref)
	if err != nil {
		slog.Error("billing: stk push failed", "landlord_id", req.LandlordID, "error", err)
		http.Error(w, "stk push failed", http.StatusBadGateway)
		return
	}

	if err := h.svc.repo.InsertSTKPush(ctx, landlord.ID, ref, checkoutID, amount); err != nil {
		slog.Error("billing: insert stk push record", "landlord_id", req.LandlordID, "error", err)
		// non-fatal — push already sent to phone
	}

	slog.Info("billing: stk push initiated",
		"landlord_id", req.LandlordID,
		"amount", amount,
		"ref", ref,
	)
	w.WriteHeader(http.StatusOK)
}

// ProcessSubscriptionFromSTK is called by the STK callback handler.
// Resolves landlord via CheckoutRequestID, marks the push row success,
// then runs the same ProcessPayment path as C2B paybill payments.
func (h *Handler) ProcessSubscriptionFromSTK(checkoutID, receipt string, amount int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ref, err := h.svc.repo.GetSTKRefByCheckoutID(ctx, checkoutID)
	if err != nil {
		slog.Error("billing: process stk subscription", "checkout_id", checkoutID, "error", err)
		return
	}

	if err := h.svc.repo.MarkSTKSuccessByCheckoutID(ctx, checkoutID, receipt); err != nil {
		slog.Error("billing: mark stk success", "checkout_id", checkoutID, "error", err)
	}

	if err := h.svc.ProcessPayment(ctx, receipt, ref, amount); err != nil {
		slog.Error("billing: activate from stk", "receipt", receipt, "error", err)
	}
}

// ProcessSubscription is called by the mpesa C2B handler
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
