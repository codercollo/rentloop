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
	"strconv"
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
func (t *Twilio) NotifyLandlord(ctx context.Context, landlordPhone string, p *models.Payment, unit *models.Unit, apartmentName string) error {
	if p == nil {
		return fmt.Errorf("twilio: payment is nil")
	}
	to := "whatsapp:" + landlordPhone
	return t.sendWhatsApp(ctx, to, buildLandlordMessage(p, unit, apartmentName))
}

// NotifyTenant sends a payment receipt to the tenant via WhatsApp.
//
// FIX 1: Previously this called buildTenantSMS which lacked a
// PaymentStatusOver case, causing overpaid tenants to receive a
// generic "KES X received. Ref: Y." message instead of a proper receipt.
// buildTenantMessage now handles paid, partial, and over statuses correctly.
func (t *Twilio) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit, apartmentName string) error {
	if phone == "" {
		return fmt.Errorf("twilio: tenant phone is empty")
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	msg := buildTenantMessage(p, unit, apartmentName)
	if msg == "" {
		return nil
	}

	// Sandbox: both channels use WhatsApp.
	// Production: switch this to t.sendSMS once you have a real SMS number.
	to := "whatsapp:" + phone
	return t.sendWhatsApp(ctx, to, msg)
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
func (t *Twilio) SendReminder(ctx context.Context, phone, tenantName, unitRef string, remaining, alreadyPaid int, month string) error {
	var msg string
	if alreadyPaid > 0 {
		msg = fmt.Sprintf(
			"Hi %s, you have paid KES %s for unit %s (%s) but KES %s is still outstanding. "+
				"Please complete your payment via M-Pesa Paybill, account: %s. Thank you.",
			tenantName,
			formatAmount(alreadyPaid),
			unitRef, month,
			formatAmount(remaining),
			unitRef,
		)
	} else {
		msg = fmt.Sprintf(
			"Hi %s, your rent of KES %s for unit %s (%s) is due. "+
				"Pay via M-Pesa Paybill, account: %s. Thank you.",
			tenantName,
			formatAmount(remaining),
			unitRef, month,
			unitRef,
		)
	}

	phone = normaliseE164(phone)
	to := "whatsapp:" + phone
	return t.sendWhatsApp(ctx, to, msg)
}

// normaliseE164 converts Kenyan local numbers to E.164 format.
func normaliseE164(phone string) string {
	phone = strings.TrimSpace(phone)
	switch {
	case strings.HasPrefix(phone, "+"):
		return phone // already E.164
	case strings.HasPrefix(phone, "254"):
		return "+" + phone // missing leading +
	case strings.HasPrefix(phone, "0") && len(phone) == 10:
		return "+254" + phone[1:] // local format: strip 0, add +254
	default:
		return "+" + phone // best-effort fallback
	}
}

// SendOnboarding sends payment instructions to a new tenant via SMS.
func (t *Twilio) SendOnboarding(ctx context.Context, phone, tenantName, unitRef, paybill, apartmentName string, expectedRent int) error {
	var msg string
	if apartmentName != "" {
		msg = fmt.Sprintf(
			"Hi %s, your landlord at %s uses RentLoop for rent. "+
				"Pay KES %d monthly to Paybill %s, account: %s. "+
				"You will receive a receipt instantly after payment.",
			tenantName, apartmentName, expectedRent, paybill, unitRef,
		)
	} else {
		msg = fmt.Sprintf(
			"Hi %s, your landlord uses RentLoop for rent. "+
				"Pay KES %d monthly to Paybill %s, account: %s. "+
				"You will receive a receipt instantly after payment.",
			tenantName, expectedRent, paybill, unitRef,
		)
	}
	// normalise phone to E.164
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

// ── Message builders ──────────────────────────────────────────────────────────

// buildTenantMessage formats the WhatsApp/SMS receipt for the tenant.
func buildTenantMessage(p *models.Payment, unit *models.Unit, apartmentName string) string {
	property := "RentLoop"
	if apartmentName != "" {
		property = apartmentName
	}

	if unit == nil {
		return fmt.Sprintf(
			"%s: KES %d received on %s. Ref: %s.",
			property,
			p.Amount,
			p.PaidAt.Format("02 Jan 2006 15:04"),
			p.TransactionID,
		)
	}

	ts := p.PaidAt.In(eatLocation()).Format("02 Jan 15:04")

	switch p.Status {
	case models.PaymentStatusPaid:
		return fmt.Sprintf(
			"%s: KES %d received for Unit %s on %s. "+
				"Rent paid in full. Receipt: #%s.",
			property, p.Amount, unit.UnitRef, ts, shortID(p.ID),
		)
	case models.PaymentStatusOver:
		// FIX 1: overpayment case was missing — fell through to default.
		// Tenant now gets a clear message showing the overpaid amount.
		return fmt.Sprintf(
			"%s: KES %d received for Unit %s on %s. "+
				"Rent paid in full (overpayment recorded). Receipt: #%s.",
			property, p.Amount, unit.UnitRef, ts, shortID(p.ID),
		)
	case models.PaymentStatusPartial:
		return fmt.Sprintf(
			"%s: KES %d received for Unit %s on %s. "+
				"Partial payment recorded. Receipt: #%s.",
			property, p.Amount, unit.UnitRef, ts, shortID(p.ID),
		)
	default:
		return fmt.Sprintf(
			"%s: KES %d received. Ref: %s.",
			property, p.Amount, p.TransactionID,
		)
	}
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

	slog.Info("twilio: message sent", "to", to, "status", resp.StatusCode, "msg", body)
	return nil
}

// formatAmount adds thousands separators — mirrors the bot package helper.
func formatAmount(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		pos := len(s) - i
		if i > 0 && pos%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
