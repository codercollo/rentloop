// Package bot implements the WhatsApp command interface for RentLoop.
//
// It receives inbound messages from Africa's Talking, identifies the
// sender as a landlord or agent, checks subscription status, parses
// the command keyword, and dispatches to the correct handler.
//
// All responses are sent back via the WhatsApp notifier.
// Panics inside goroutines are recovered and logged — the handler
// never crashes the server.
package bot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ATInboundMessage is the payload Africa's Talking POSTs for
// every inbound WhatsApp message.
type ATInboundMessage struct {
	To   string `json:"to"`
	From string `json:"from"`
	Text string `json:"text"`
	ID   string `json:"id"`
	Date string `json:"date"`
}

// Handler is the HTTP handler for inbound WhatsApp messages.
type Handler struct {
	svc *Service
}

// NewHandler wires the service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Inbound handles POST /bot/whatsapp (Africa's Talking inbound webhook).
//
// Production guarantees:
//   - Always returns 200 to AT so it does not retry.
//   - Command processing is async — response time is <100ms.
//   - Panics inside the goroutine are recovered and logged.
func (h *Handler) Inbound(w http.ResponseWriter, r *http.Request) {
	var msg ATInboundMessage

	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		// AT also sends form-encoded — try that fallback
		if err2 := r.ParseForm(); err2 == nil {
			msg.From = r.FormValue("from")
			msg.To = r.FormValue("to")
			msg.Text = r.FormValue("text")
			msg.ID = r.FormValue("id")
		}
	}

	msg.From = strings.TrimSpace(msg.From)
	msg.Text = strings.TrimSpace(msg.Text)

	if msg.From == "" || msg.Text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("bot: inbound message",
		"from", msg.From,
		"text", msg.Text,
		"id", msg.ID,
	)

	// Always acknowledge AT immediately
	w.WriteHeader(http.StatusOK)

	// Process async — never block the HTTP response
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("bot: panic recovered",
					"from", msg.From,
					"text", msg.Text,
					"panic", rec,
				)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		h.svc.Handle(ctx, msg.From, msg.Text)
	}()
}
