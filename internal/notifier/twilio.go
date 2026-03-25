// Package notifier — Twilio outbound WhatsApp and SMS.
//
// Replaces the Africa's Talking WhatsApp notifier.
// AT SMS stays for tenant receipts if preferred, but Twilio
// handles both channels from one account.
package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

const twilioAPIBase = "https://api.twilio.com/2010-04-01/Accounts"

// Twilio sends WhatsApp and SMS messages via Twilio.
type Twilio struct {
	accountSID   string
	authToken    string
	whatsappFrom string // e.g. "whatsapp:+14155238886"
	smsFrom      string // e.g. "+14155238886"
	client       *http.Client
	baseURL      string // override in tests
}

// NewTwilio returns a configured Twilio notifier.
func NewTwilio(accountSID, authToken, whatsappFrom, smsFrom string) *Twilio {
	return &Twilio{
		accountSID:   accountSID,
		authToken:    authToken,
		whatsappFrom: whatsappFrom,
		smsFrom:      smsFrom,
		client:       &http.Client{Timeout: 15 * time.Second},
	}
}

// NewTwilioWithURL is used in tests to inject a custom endpoint.
func NewTwilioWithURL(baseURL, accountSID, authToken, whatsappFrom, smsFrom string) *Twilio {
	t := NewTwilio(accountSID, authToken, whatsappFrom, smsFrom)
	t.baseURL = baseURL
	return t
}

// ── WhatsApp ─────────────────────────────────────────────────────────────────

// NotifyLandlord sends a payment notification to the landlord via WhatsApp.
func (t *Twilio) NotifyLandlord(ctx context.Context, landlordPhone string, p *models.Payment, unit *models.Unit) error {
	if p == nil {
		return fmt.Errorf("twilio: payment is nil")
	}
	to := "whatsapp:" + landlordPhone
	return t.sendWhatsApp(ctx, to, buildLandlordMessage(p, unit))
}

// SendRaw sends a plain WhatsApp message — used by the bot sender adapter.
func (t *Twilio) SendRaw(ctx context.Context, to, message string) error {
	// Normalise: add whatsapp: prefix if not present
	if !strings.HasPrefix(to, "whatsapp:") {
		to = "whatsapp:" + to
	}
	return t.sendWhatsApp(ctx, to, message)
}

func (t *Twilio) sendWhatsApp(ctx context.Context, to, body string) error {
	return t.post(ctx, to, t.whatsappFrom, body)
}

// ── SMS ───────────────────────────────────────────────────────────────────────

func (t *Twilio) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	if phone == "" {
		return fmt.Errorf("twilio: tenant phone is empty")
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	//TODO:
	// Sandbox: both channels use WhatsApp
	// Production: switch this to t.sendSMS once you have a real SMS number
	to := "whatsapp:" + phone
	return t.sendWhatsApp(ctx, to, buildTenantSMS(p, unit))
}

// NotifyLandlordSMS sends a plain SMS notification to the landlord.
// Fallback when WhatsApp is not available.
func (t *Twilio) NotifyLandlordSMS(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	var msg string
	if unit == nil {
		msg = fmt.Sprintf("RentLoop: Unmatched payment KES %d. Ref unknown. Txn: %s", p.Amount, p.TransactionID)
	} else {
		msg = fmt.Sprintf("RentLoop: %s (Unit %s) paid KES %d. Status: %s. Ref: #%s",
			unit.TenantName, unit.UnitRef, p.Amount, p.Status, shortID(p.ID))
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	return t.sendSMS(ctx, phone, msg)
}

// SendReminder sends a payment reminder SMS to an unpaid tenant.
func (t *Twilio) SendReminder(ctx context.Context, phone, tenantName, unitRef string, expectedRent int, month string) error {
	msg := fmt.Sprintf(
		"Hi %s, your rent of KES %d for unit %s (%s) is due. "+
			"Pay via M-Pesa Paybill, account: %s. Thank you.",
		tenantName, expectedRent, unitRef, month, unitRef,
	)
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	// TODO: switch to t.sendSMS in production
	to := "whatsapp:" + phone
	return t.sendWhatsApp(ctx, to, msg)
}

// SendOnboarding sends payment instructions to a new tenant via SMS.
func (t *Twilio) SendOnboarding(ctx context.Context, phone, tenantName, unitRef, paybill string, expectedRent int) error {
	msg := fmt.Sprintf(
		"Hi %s, your landlord uses RentLoop. "+
			"Pay KES %d monthly to Paybill %s, account: %s. "+
			"You will receive a receipt instantly after payment.",
		tenantName, expectedRent, paybill, unitRef,
	)
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	return t.sendSMS(ctx, phone, msg)
}

// SendRawSMS sends a plain SMS — used by the smsSender adapter.
func (t *Twilio) SendRawSMS(ctx context.Context, to, message string) error {
	if !strings.HasPrefix(to, "+") {
		to = "+" + to
	}
	return t.sendSMS(ctx, to, message)
}

func (t *Twilio) sendSMS(ctx context.Context, to, body string) error {
	return t.post(ctx, to, t.smsFrom, body)
}

// ── Core HTTP ─────────────────────────────────────────────────────────────────

func (t *Twilio) post(ctx context.Context, to, from, body string) error {
	endpoint := fmt.Sprintf("%s/%s/Messages.json", twilioAPIBase, t.accountSID)
	if t.baseURL != "" {
		endpoint = t.baseURL
	}

	data := url.Values{}
	data.Set("To", to)
	data.Set("From", from)
	data.Set("Body", body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("twilio: build request: %w", err)
	}
	req.SetBasicAuth(t.accountSID, t.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("twilio: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		var tw struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		}
		_ = json.Unmarshal(b, &tw)
		return fmt.Errorf("twilio: %d — %s (code %d)", resp.StatusCode, tw.Message, tw.Code)
	}

	slog.Info("twilio: message sent", "to", to, "status", resp.StatusCode)
	return nil
}
