// Package notifier sends outbound WhatsApp and SMS messages via external APIs,
// including Africa's Talking. It provides helpers to send raw messages and
// structured notifications such as landlord payment alerts.
package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	baseURL  string // empty = production AT URL; set in tests to a local server
}

// NewWhatsApp returns a configured WhatsApp notifier pointed at Africa's Talking.
func NewWhatsApp(apiKey, username, from string) *WhatsApp {
	return &WhatsApp{
		apiKey:   apiKey,
		username: username,
		from:     from,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// NewWhatsAppWithURL is used in tests to inject a custom endpoint URL
// instead of the real Africa's Talking API.
func NewWhatsAppWithURL(url, apiKey, username, from string) *WhatsApp {
	return &WhatsApp{
		apiKey:   apiKey,
		username: username,
		from:     from,
		baseURL:  url,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// NotifyLandlord sends a payment notification to the landlord's WhatsApp.
func (w *WhatsApp) NotifyLandlord(ctx context.Context, landlordPhone string, p *models.Payment, unit *models.Unit, apartmentName string) error {
	if p == nil {
		return fmt.Errorf("whatsapp: payment is nil")
	}
	return w.send(ctx, landlordPhone, buildLandlordMessage(p, unit, apartmentName))
}

// SendRaw sends a plain text message to any WhatsApp number.
// Used by the bot package which composes its own message strings.
func (w *WhatsApp) SendRaw(ctx context.Context, to, message string) error {
	return w.send(ctx, to, message)
}

// send POSTs a form-encoded message via Africa's Talking.
func (w *WhatsApp) send(ctx context.Context, to, message string) error {
	url := atWhatsAppURL
	if w.baseURL != "" {
		url = w.baseURL
	}

	data := "username=" + w.username +
		"&to=" + to +
		"&message=" + message +
		"&from=" + w.from

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(data))
	if err != nil {
		return fmt.Errorf("whatsapp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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

// ── Message builders ──────────────────────────────────────────────────────────

func buildLandlordMessage(p *models.Payment, unit *models.Unit, apartmentName string) string {
	property := "RentLoop"
	if apartmentName != "" {
		property = apartmentName
	}

	if unit == nil {
		return fmt.Sprintf(
			"*%s* — Unmatched payment\n"+
				"Amount: KES %d\n"+
				"From: %s\n"+
				"Transaction: %s\n\n"+
				"Reply *CLAIM %s TO <unit>* to assign it.",
			property,
			p.Amount, p.TenantPhone, p.TransactionID, p.TransactionID,
		)
	}

	msg := fmt.Sprintf(
		"*%s* %s\n"+
			"%s (Unit %s) paid KES %d\n"+
			"Status: %s\n"+
			"Time: %s\n"+
			"Receipt: #%s",
		property,
		statusEmoji(p.Status),
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
