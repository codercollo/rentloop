package billing

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Handler routes subscription payments to the billing service.
type Handler struct {
	svc *Service
}

// NewHandler wires the service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
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
