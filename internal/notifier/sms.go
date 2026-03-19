// Package notifier — SMS outbound via Africa's Talking.
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
func (s *SMS) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	if phone == "" {
		return fmt.Errorf("sms: tenant phone is empty")
	}
	return s.send(ctx, phone, buildTenantSMS(p, unit))
}

// SendReminder sends a payment reminder to an unpaid tenant.
func (s *SMS) SendReminder(ctx context.Context, phone, tenantName, unitRef string, expectedRent int, month string) error {
	msg := fmt.Sprintf(
		"Hi %s, your rent of KES %d for unit %s (%s) is due. "+
			"Pay via M-Pesa Paybill, account: %s. Thank you.",
		tenantName, expectedRent, unitRef, month, unitRef,
	)
	return s.send(ctx, phone, msg)
}

// SendOnboarding sends one-time payment instructions to a new tenant.
func (s *SMS) SendOnboarding(ctx context.Context, phone, tenantName, unitRef, paybill string, expectedRent int) error {
	msg := fmt.Sprintf(
		"Hi %s, your landlord uses RentLoop for rent. "+
			"Pay KES %d monthly to Paybill %s, account: %s. "+
			"You will receive a receipt instantly after payment.",
		tenantName, expectedRent, paybill, unitRef,
	)
	return s.send(ctx, phone, msg)
}

// buildTenantSMS formats the SMS receipt for the tenant.
func buildTenantSMS(p *models.Payment, unit *models.Unit) string {
	if unit == nil {
		return fmt.Sprintf(
			"RentLoop: KES %d received on %s. Ref: %s.",
			p.Amount,
			p.PaidAt.Format("02 Jan 2006 15:04"),
			p.TransactionID,
		)
	}

	ts := p.PaidAt.In(eatLocation()).Format("02 Jan 15:04")

	switch p.Status {
	case models.PaymentStatusPaid, models.PaymentStatusOver:
		return fmt.Sprintf(
			"RentLoop: KES %d received for Unit %s on %s. "+
				"Rent paid in full. Receipt: #%s.",
			p.Amount, unit.UnitRef, ts, shortID(p.ID),
		)
	case models.PaymentStatusPartial:
		return fmt.Sprintf(
			"RentLoop: KES %d received for Unit %s on %s. "+
				"Partial payment recorded. Receipt: #%s.",
			p.Amount, unit.UnitRef, ts, shortID(p.ID),
		)
	default:
		return fmt.Sprintf(
			"RentLoop: KES %d received. Ref: %s.",
			p.Amount, p.TransactionID,
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

// SendActivationSMS sends a raw SMS for sandbox activation.
func (s *SMS) SendActivationSMS(ctx context.Context, to, message string) error {
	return s.send(ctx, to, message)
}
