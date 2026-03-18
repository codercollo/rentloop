// Package notifier handles all outbound WhatsApp and SMS messages
// via the Africa's Talking API.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

const atWhatsAppURL = "https://api.africastalking.com/version1/messaging/whatsapp"

// WhatsApp sends outbound WhatsApp messages via Africa's Talking.
type WhatsApp struct {
	apiKey   string
	username string
	from     string
	client   *http.Client
}

// NewWhatsApp returns a configured WhatsApp notifier.
func NewWhatsApp(apiKey, username, from string) *WhatsApp {
	return &WhatsApp{
		apiKey:   apiKey,
		username: username,
		from:     from,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// NotifyLandlord sends a payment notification to the landlord's WhatsApp.
// When unit is nil the payment was unmatched — the message reflects that.
func (w *WhatsApp) NotifyLandlord(ctx context.Context, landlordPhone string, p *models.Payment, unit *models.Unit) error {
	if p == nil {
		return fmt.Errorf("whatsapp: payment is nil")
	}
	msg := buildLandlordMessage(p, unit)
	return w.send(ctx, landlordPhone, msg)
}

// buildLandlordMessage formats the WhatsApp notification for the landlord.
func buildLandlordMessage(p *models.Payment, unit *models.Unit) string {
	if unit == nil {
		return fmt.Sprintf(
			"*RentLoop* — Unmatched payment\n"+
				"Amount: KES %d\n"+
				"From: %s\n"+
				"Transaction: %s\n\n"+
				"Reply *CLAIM %s TO <unit>* to assign it.",
			p.Amount, p.TenantPhone, p.TransactionID, p.TransactionID,
		)
	}

	emoji := statusEmoji(p.Status)

	msg := fmt.Sprintf(
		"*RentLoop* %s\n"+
			"%s (Unit %s) paid KES %d\n"+
			"Status: %s\n"+
			"Time: %s\n"+
			"Receipt: #%s",
		emoji,
		unit.TenantName, unit.UnitRef, p.Amount,
		formatStatus(p.Status),
		p.PaidAt.In(eatLocation()).Format("02 Jan 15:04"),
		shortID(p.ID),
	)

	if p.Status == models.PaymentStatusPartial {
		msg += "\n\n_Partial payment — unit not yet fully paid._"
	}

	return msg
}

// send POSTs a WhatsApp message via Africa's Talking.
func (w *WhatsApp) send(ctx context.Context, to, message string) error {
	payload := map[string]string{
		"username": w.username,
		"to":       to,
		"message":  message,
		"from":     w.from,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("whatsapp: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, atWhatsAppURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("whatsapp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apiKey", w.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("whatsapp: AT returned %d", resp.StatusCode)
	}

	slog.Info("whatsapp: message sent", "to", to, "status", resp.StatusCode)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func statusEmoji(s models.PaymentStatus) string {
	switch s {
	case models.PaymentStatusPaid, models.PaymentStatusOver:
		return "✓"
	case models.PaymentStatusPartial:
		return "⚠"
	default:
		return "•"
	}
}

func formatStatus(s models.PaymentStatus) string {
	switch s {
	case models.PaymentStatusPaid:
		return "Paid in full"
	case models.PaymentStatusPartial:
		return "Partial"
	case models.PaymentStatusOver:
		return "Overpaid"
	default:
		return string(s)
	}
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func eatLocation() *time.Location {
	loc, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		return time.FixedZone("EAT", 3*60*60)
	}
	return loc
}
