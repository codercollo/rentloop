package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// BillingRepository is the persistence interface the service depends on.
type BillingRepository interface {
	GetLandlordByID(ctx context.Context, id string) (*models.Landlord, error)
	GetExpiredPaidAccounts(ctx context.Context) ([]models.Landlord, error)
	GetGraceAccounts(ctx context.Context, graceDays int) ([]models.Landlord, error)
	SetStatus(ctx context.Context, landlordID string, status models.SubscriptionStatus) error
	Activate(ctx context.Context, landlordID string) error
	RecordPayment(ctx context.Context, p models.SubscriptionPayment) error
	GetLandlordByRef(ctx context.Context, ref string) (*models.Landlord, error)
}

// Notifier sends WhatsApp billing notices.
type Notifier interface {
	Send(ctx context.Context, to, message string) error
}

// Service handles subscription lifecycle.
type Service struct {
	repo          BillingRepository
	notifier      Notifier
	graceDays     int
	pricePerUnit  int
	freeTierLimit int
}

// NewService wires all dependencies.
func NewService(repo BillingRepository, notifier Notifier, graceDays, pricePerUnit, freeTierLimit int) *Service {
	return &Service{
		repo:          repo,
		notifier:      notifier,
		graceDays:     graceDays,
		pricePerUnit:  pricePerUnit,
		freeTierLimit: freeTierLimit,
	}
}

// MonthlyAmount returns the billing amount for a given unit count.
func (s *Service) MonthlyAmount(unitCount int) int {
	if unitCount <= s.freeTierLimit {
		return 0
	}
	return unitCount * s.pricePerUnit
}

// IsFree returns true when unit count is within the free tier.
func (s *Service) IsFree(unitCount int) bool {
	return unitCount <= s.freeTierLimit
}

// SubscriptionRef returns the M-Pesa account reference for a landlord's subscription.
func SubscriptionRef(landlordID string) string {
	short := landlordID
	if len(short) > 8 {
		short = short[:8]
	}
	return "RENTLOOP-" + short
}

// IsSubscriptionPayment returns true when a C2B account ref is a subscription payment.
func IsSubscriptionPayment(ref string) bool {
	return len(ref) > 9 && ref[:9] == "RENTLOOP-"
}

// ProcessPayment handles an incoming subscription C2B payment.
// Called from the mpesa handler when account ref starts with RENTLOOP-.
func (s *Service) ProcessPayment(ctx context.Context, transactionID, ref string, amount int) error {
	landlord, err := s.repo.GetLandlordByRef(ctx, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			slog.Warn("billing: subscription payment — landlord not found", "ref", ref)
			return nil
		}
		return fmt.Errorf("billing: get landlord: %w", err)
	}

	expected := s.MonthlyAmount(landlord.UnitCount)
	if amount < expected {
		slog.Warn("billing: underpayment",
			"landlord_id", landlord.ID,
			"expected", expected,
			"received", amount,
		)
		// Notify landlord of shortfall
		msg := fmt.Sprintf(
			"*RentLoop — Partial Subscription Payment*\n\n"+
				"Received KES %d but your monthly fee is KES %d.\n"+
				"Pay the remaining KES %d to Paybill %s, account: %s\n"+
				"to restore full access.",
			amount, expected, expected-amount,
			landlord.PaybillNumber, ref,
		)
		_ = s.notifier.Send(ctx, landlord.WhatsAppPhone, msg)
		return nil
	}

	now := time.Now()
	payment := models.SubscriptionPayment{
		LandlordID:    landlord.ID,
		TransactionID: transactionID,
		Amount:        amount,
		PeriodStart:   now,
		PeriodEnd:     now.AddDate(0, 1, 0),
	}

	if err := s.repo.RecordPayment(ctx, payment); err != nil {
		return fmt.Errorf("billing: record payment: %w", err)
	}

	if err := s.repo.Activate(ctx, landlord.ID); err != nil {
		return fmt.Errorf("billing: activate: %w", err)
	}

	slog.Info("billing: account activated",
		"landlord_id", landlord.ID,
		"amount", amount,
	)

	msg := fmt.Sprintf(
		"*RentLoop — Subscription Active* ✓\n\n"+
			"KES %d received. Your account is active until %s.\n"+
			"Thank you!",
		amount, now.AddDate(0, 1, 0).Format("02 Jan 2006"),
	)
	if s.notifier != nil {
		_ = s.notifier.Send(ctx, landlord.WhatsAppPhone, msg)
	}
	return nil
}

// TransitionExpired moves active paid accounts to grace when their cycle ends.
// Called by cron on the 1st of the month.
func (s *Service) TransitionExpired(ctx context.Context) error {
	landlords, err := s.repo.GetExpiredPaidAccounts(ctx)
	if err != nil {
		return fmt.Errorf("billing: get expired: %w", err)
	}

	for _, l := range landlords {
		if s.IsFree(l.UnitCount) {
			continue
		}

		if err := s.repo.SetStatus(ctx, l.ID, models.StatusGrace); err != nil {
			slog.Error("billing: set grace failed", "landlord_id", l.ID, "error", err)
			continue
		}

		amount := s.MonthlyAmount(l.UnitCount)
		msg := fmt.Sprintf(
			"*RentLoop — Payment Due* ⚠\n\n"+
				"Your subscription has expired.\n"+
				"Pay KES %d to Paybill %s, account: %s\n\n"+
				"You have %d days before your account is suspended.\n"+
				"All your data is safe.",
			amount, l.PaybillNumber, SubscriptionRef(l.ID), s.graceDays,
		)
		if s.notifier != nil {
			_ = s.notifier.Send(ctx, l.WhatsAppPhone, msg)
		}

		slog.Info("billing: moved to grace", "landlord_id", l.ID)
	}
	return nil
}

// TransitionGrace moves grace accounts to suspended when grace period ends.
// Called by cron 6 days after the 1st.
func (s *Service) TransitionGrace(ctx context.Context) error {
	landlords, err := s.repo.GetGraceAccounts(ctx, s.graceDays)
	if err != nil {
		return fmt.Errorf("billing: get grace: %w", err)
	}

	for _, l := range landlords {
		if s.IsFree(l.UnitCount) {
			continue
		}

		if err := s.repo.SetStatus(ctx, l.ID, models.StatusSuspended); err != nil {
			slog.Error("billing: suspend failed", "landlord_id", l.ID, "error", err)
			continue
		}

		amount := s.MonthlyAmount(l.UnitCount)
		msg := fmt.Sprintf(
			"*RentLoop — Account Suspended* ✗\n\n"+
				"Your subscription has lapsed.\n"+
				"Pay KES %d to Paybill %s, account: %s\n\n"+
				"Your payment history is safe and will be restored on payment.",
			amount, l.PaybillNumber, SubscriptionRef(l.ID),
		)
		if s.notifier != nil {
			_ = s.notifier.Send(ctx, l.WhatsAppPhone, msg)
		}

		slog.Info("billing: suspended", "landlord_id", l.ID)
	}
	return nil
}
