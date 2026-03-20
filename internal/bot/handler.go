package bot

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Handler is the HTTP handler for inbound WhatsApp messages.
type Handler struct {
	svc *Service
}

// NewHandler wires the service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Inbound handles POST /bot/whatsapp.
//
// Supports both Twilio (form-encoded, From/Body) and
// Africa's Talking (form-encoded, from/text).
// Always returns 200 immediately — processing is async.
func (h *Handler) Inbound(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.Error("bot handler: parse form failed", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Twilio sends "From" and "Body" (capital)
	// AT sends "from" and "text" (lowercase)
	from := r.FormValue("From")
	text := r.FormValue("Body")
	id := r.FormValue("MessageSid")

	// AT fallback
	if from == "" {
		from = r.FormValue("from")
	}
	if text == "" {
		text = r.FormValue("text")
	}
	if id == "" {
		id = r.FormValue("id")
	}

	// Twilio prefixes sender with "whatsapp:" — strip it
	from = strings.TrimPrefix(strings.TrimSpace(from), "whatsapp:")
	text = strings.TrimSpace(text)

	if from == "" || text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("bot: inbound message", "from", from, "text", text, "id", id)

	w.WriteHeader(http.StatusOK)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("bot: panic recovered",
					"from", from, "text", text, "panic", rec)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.svc.Handle(ctx, from, text)
	}()
}
