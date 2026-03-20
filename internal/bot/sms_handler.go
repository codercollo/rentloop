// Package bot — SMS inbound handler.
//
// Mirrors the WhatsApp handler exactly:
// immediate 200 ACK → async goroutine → shared Service logic.
// AT SMS webhooks are form-encoded, not JSON.
package bot

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// SMSHandler handles inbound SMS messages from Africa's Talking.
type SMSHandler struct {
	svc *Service
}

// NewSMSHandler wires the service.
func NewSMSHandler(svc *Service) *SMSHandler {
	return &SMSHandler{svc: svc}
}

// Inbound handles POST /bot/sms (Africa's Talking SMS webhook).
//
// Same guarantees as the WhatsApp handler:
//   - Always returns 200 immediately so AT does not retry.
//   - Processing is async.
//   - Panics in the goroutine are recovered and logged.
func (h *SMSHandler) Inbound(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.Error("sms handler: parse form failed", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	from := strings.TrimSpace(r.FormValue("from"))
	text := strings.TrimSpace(r.FormValue("text"))
	id := r.FormValue("id")

	if from == "" || text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("sms handler: inbound",
		"from", from,
		"text", text,
		"id", id,
	)

	w.WriteHeader(http.StatusOK)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("sms handler: panic recovered",
					"from", from,
					"text", text,
					"panic", rec,
				)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		h.svc.HandleSMS(ctx, from, text)
	}()
}
