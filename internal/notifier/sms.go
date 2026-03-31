// Package notifier provides SMS messaging via Africa's Talking,
// including tenant notifications, landlord alerts, reminders,
// onboarding messages, and raw SMS sending utilities.
package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

const atSMSURL = "https://api.sandbox.africastalking.com/version1/messaging"

// SMS sends outbound SMS messages via Africa's Talking.
type SMS struct {
	apiKey   string
	username string
	senderID string
	client   *http.Client
}

// NewSMS returns a configured SMS notifier.
func NewSMS(apiKey, username, senderID string) *SMS {
	return &SMS{
		apiKey:   apiKey,
		username: username,
		senderID: senderID,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// NotifyTenant sends an SMS receipt to the tenant after a matched payment.
// apartmentName is passed from the landlord record.
func (s *SMS) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit, apartmentName string) error {
	if phone == "" {
		return fmt.Errorf("sms: tenant phone is empty")
	}
	return s.send(ctx, phone, buildTenantSMS(p, unit, apartmentName))
}

// NotifyLandlordSMS sends an SMS notification to the landlord.
// Used as WhatsApp fallback until AT WhatsApp is provisioned.
func (s *SMS) NotifyLandlordSMS(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	var msg string
	if unit == nil {
		msg = fmt.Sprintf(
			"RentLoop: Unmatched payment KES %d received. Ref unknown. Reply CLAIM to assign. Txn: %s",
			p.Amount, p.TransactionID,
		)
	} else {
		msg = fmt.Sprintf(
			"RentLoop: %s (Unit %s) paid KES %d. Status: %s. Ref: #%s",
			unit.TenantName, unit.UnitRef, p.Amount, p.Status, shortID(p.ID),
		)
	}
	return s.send(ctx, phone, msg)
}

// SendReminder sends a payment reminder to an unpaid tenant.
func (s *SMS) SendReminder(ctx context.Context, phone, tenantName, unitRef string, remaining, alreadyPaid int, month string) error {
	var msg string
	if alreadyPaid > 0 {
		msg = fmt.Sprintf(
			"Hi %s, you have paid KES %s for unit %s (%s) but KES %s is still outstanding. "+
				"Pay via M-Pesa Paybill, account: %s. Thank you.",
			tenantName, formatAmount(alreadyPaid), unitRef, month, formatAmount(remaining), unitRef,
		)
	} else {
		msg = fmt.Sprintf(
			"Hi %s, your rent of KES %s for unit %s (%s) is due. "+
				"Pay via M-Pesa Paybill, account: %s. Thank you.",
			tenantName, formatAmount(remaining), unitRef, month, unitRef,
		)
	}
	return s.send(ctx, phone, msg)
}

// SendOnboarding sends one-time payment instructions to a new tenant.
func (s *SMS) SendOnboarding(ctx context.Context, phone, tenantName, unitRef, paybill, apartmentName string, expectedRent int) error {
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
	return s.send(ctx, phone, msg)
}

// SendRaw sends a plain text SMS.
// Used by the smsSender adapter in main.go so bot and onboarding
// welcome messages go via SMS until AT WhatsApp is provisioned.
func (s *SMS) SendRaw(ctx context.Context, to, message string) error {
	return s.send(ctx, to, message)
}

// buildTenantSMS formats the SMS receipt for the tenant (Africa's Talking path).
//
// Note: The Twilio path now uses buildTenantMessage (in twilio.go) which
// includes a PaymentStatusOver case. This function is retained for the AT
// SMS path and has been updated with the same Over case for consistency.
func buildTenantSMS(p *models.Payment, unit *models.Unit, apartmentName string) string {
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
		// FIX 1 (parity): Added overpayment case to match buildTenantMessage.
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

// send POSTs an SMS via Africa's Talking.
func (s *SMS) send(ctx context.Context, to, message string) error {
	data := url.Values{}
	data.Set("username", s.username)
	data.Set("to", to)
	data.Set("message", message)
	data.Set("from", s.senderID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, atSMSURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("sms: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("apiKey", s.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sms: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("sms: AT returned %d", resp.StatusCode)
	}

	slog.Info("sms: message sent", "to", to, "status", resp.StatusCode)
	return nil
}

// SendActivationSMS sends a raw SMS — used by the sandbox activation script.
func (s *SMS) SendActivationSMS(ctx context.Context, to, message string) error {
	return s.send(ctx, to, message)
}
