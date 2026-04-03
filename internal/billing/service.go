package billing

import (
	"context"
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
	GetSTKRefByReceipt(ctx context.Context, receipt string) (string, error)
	MarkSTKSuccess(ctx context.Context, receipt string, landlordID string) error
	InsertSTKPush(ctx context.Context, landlordID, ref, checkoutID string, amount int) error
	RecordPayment(ctx context.Context, p models.SubscriptionPayment) error
	GetLandlordByRef(ctx context.Context, ref string) (*models.Landlord, error)
	SubscriptionPaymentExists(ctx context.Context, transactionID string) (bool, error)
	GetSTKRefByCheckoutID(ctx context.Context, checkoutID string) (string, error)
	MarkSTKSuccessByCheckoutID(ctx context.Context, checkoutID, receipt string) error
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
// Safe to call multiple times with the same transactionID — subsequent calls
// are silently ignored (idempotent). This fixes BUG-3 where duplicate curls
// caused a second "Subscription Active ✓" WhatsApp to be sent.
func (s *Service) ProcessPayment(ctx context.Context, transactionID, ref string, amount int) error {
	// ── Resolve landlord from subscription ref ────────────────────────────────
	// GetLandlordByRef matches LEFT(id::text, 8) = RIGHT(ref, 8),
	// e.g. ref "RENTLOOP-accee42b" → landlord whose id starts with "accee42b".
	landlord, err := s.repo.GetLandlordByRef(ctx, ref)
	if err != nil {
		return fmt.Errorf("billing: resolve landlord from ref %q: %w", ref, err)
	}

	// ── Amount check ──────────────────────────────────────────────────────────
	required := s.MonthlyAmount(landlord.UnitCount)
	if required > 0 && amount < required {
		slog.Warn("billing: underpayment",
			"landlord_id", landlord.ID,
			"expected", required,
			"received", amount,
		)
		// Notify landlord of the shortfall but do NOT activate.
		if s.notifier != nil {
			msg := fmt.Sprintf(
				"*RentLoop — Partial Subscription Payment*\n\n"+
					"Received KES %d but your monthly fee is KES %d.\n"+
					"Pay the remaining KES %d to Paybill %s, account: %s\n"+
					"to restore full access.",
				amount, required, required-amount,
				landlord.PaybillNumber, ref,
			)
			_ = s.notifier.Send(ctx, landlord.WhatsAppPhone, msg)
		}
		return nil
	}

	// ── BUG-3 FIX: idempotency guard ─────────────────────────────────────────
	// If this transaction_id was already processed, return immediately.
	// Without this check, a duplicate curl (or Safaricom retry) calls
	// Activate again and sends a second "Subscription Active ✓" WhatsApp.
	// The INSERT has ON CONFLICT DO NOTHING so the row is safe, but the
	// Activate + notifier.Send still fired on every call.
	exists, err := s.repo.SubscriptionPaymentExists(ctx, transactionID)
	if err != nil {
		return fmt.Errorf("billing: idempotency check for %s: %w", transactionID, err)
	}
	if exists {
		slog.Info("billing: duplicate subscription payment skipped",
			"transaction_id", transactionID,
			"landlord_id", landlord.ID,
		)
		return nil
	}

	// ── Record payment ────────────────────────────────────────────────────────
	now := time.Now()
	if err := s.repo.RecordPayment(ctx, models.SubscriptionPayment{
		LandlordID:    landlord.ID,
		TransactionID: transactionID,
		Amount:        amount,
		PeriodStart:   now,
		PeriodEnd:     now.AddDate(0, 1, 0),
	}); err != nil {
		// RecordPayment uses ON CONFLICT DO NOTHING, so if a concurrent
		// goroutine raced past the exists check and inserted first, this
		// is a no-op. Log and continue to activation.
		slog.Warn("billing: record payment skipped (race or conflict)",
			"transaction_id", transactionID,
		)
	}

	// ── Activate landlord ─────────────────────────────────────────────────────
	if err := s.repo.Activate(ctx, landlord.ID); err != nil {
		return fmt.Errorf("billing: activate landlord %s: %w", landlord.ID, err)
	}

	slog.Info("billing: account activated",
		"landlord_id", landlord.ID,
		"amount", amount,
	)

	// ── Notify landlord ───────────────────────────────────────────────────────
	if s.notifier != nil {
		cycleEnd := now.AddDate(0, 1, 0)
		msg := fmt.Sprintf(
			"*RentLoop — Subscription Active* ✓\n\n"+
				"KES %d received. Your account is active until %s.\n"+
				"Thank you!",
			amount,
			cycleEnd.Format("02 Jan 2006"),
		)
		_ = s.notifier.Send(ctx, landlord.WhatsAppPhone, msg)
	}

	return nil
}

// ProcessPaymentByReceipt handles an STK success where we have the M-Pesa
// receipt and amount but need to resolve the landlord via a pending STK record.
func (s *Service) ProcessPaymentByReceipt(ctx context.Context, receipt string, amount int) error {
	ref, err := s.repo.GetSTKRefByReceipt(ctx, receipt)
	if err != nil {
		return fmt.Errorf("stk: resolve ref for receipt %s: %w", receipt, err)
	}
	return s.ProcessPayment(ctx, receipt, ref, amount)
}

// TransitionExpired moves active paid accounts to grace when their cycle ends.
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

// TransitionGrace moves grace accounts to suspended when the grace period ends.
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
