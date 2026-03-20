// Package ledger implements rent payment recording and status derivation.
//
// The service layer contains all business logic for classifying payments
// as paid, partial, overpaid, or unmatched. It enforces idempotency by
// treating models.ErrDuplicate from the repository as a silent no-op.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// PaymentRepository is the persistence interface the service depends on.
// The concrete implementation is Repository. Tests inject a mock.
type PaymentRepository interface {
	InsertPayment(ctx context.Context, p models.Payment) (*models.Payment, error)
	GetMonthlyTotal(ctx context.Context, unitID, monthKey string) (int, error)
	GetUnit(ctx context.Context, unitID string) (*models.Unit, error)
}

// Service handles all payment recording logic.
type Service struct {
	repo PaymentRepository
}

// NewService wires the repository.
func NewService(repo PaymentRepository) *Service {
	return &Service{repo: repo}
}

// Record persists a payment and derives its status.
//
// Status rules:
//   - unmatched  — UnitID is empty (no unit found for the account ref)
//   - paid       — running monthly total >= expected_rent
//   - partial    — running monthly total > 0 but < expected_rent
//   - overpaid   — running monthly total >= 2 × expected_rent
//
// Idempotency: when transaction_id already exists (models.ErrDuplicate),
// Record returns (nil, nil). The caller treats this as a silent no-op.
// Daraja may fire the same callback more than once — we discard duplicates
// at the database level via the UNIQUE constraint.
func (s *Service) Record(ctx context.Context, p models.Payment) (*models.Payment, error) {
	if p.PaidAt.IsZero() {
		p.PaidAt = time.Now()
	}

	// Unmatched — no unit found. Store with no unit, status unmatched.
	if p.UnitID == "" {
		p.Status = "unmatched"
		return s.insert(ctx, p)
	}

	// Fetch the unit to read expected_rent.
	unit, err := s.repo.GetUnit(ctx, p.UnitID)
	if err != nil {
		return nil, fmt.Errorf("record: get unit: %w", err)
	}

	// Sum all payments already recorded for this unit this month.
	existing, err := s.repo.GetMonthlyTotal(ctx, p.UnitID, p.MonthKey)
	if err != nil {
		return nil, fmt.Errorf("record: get monthly total: %w", err)
	}

	newTotal := existing + p.Amount
	p.Status = deriveStatus(newTotal, unit.ExpectedRent)

	recorded, err := s.insert(ctx, p)
	if err != nil {
		return nil, err
	}

	if recorded != nil {
		slog.Info("ledger: payment recorded",
			"payment_id", recorded.ID,
			"unit", unit.UnitRef,
			"amount", recorded.Amount,
			"expected", unit.ExpectedRent,
			"monthly_total", newTotal,
			"status", recorded.Status,
		)
	}

	return recorded, nil
}

// insert calls the repository and handles the duplicate case uniformly.
// Returns (nil, nil) on duplicate — caller treats as no-op.
func (s *Service) insert(ctx context.Context, p models.Payment) (*models.Payment, error) {
	recorded, err := s.repo.InsertPayment(ctx, p)
	if err != nil {
		if errors.Is(err, models.ErrDuplicate) {
			slog.Info("ledger: duplicate transaction ignored",
				"transaction_id", p.TransactionID,
			)
			return nil, nil
		}
		return nil, fmt.Errorf("insert payment: %w", err)
	}
	return recorded, nil
}

// deriveStatus determines payment status from the running monthly total
// vs the expected rent amount.
func deriveStatus(total, expected int) models.PaymentStatus {
	switch {
	case total >= expected*2:
		return models.PaymentStatusOver
	case total >= expected:
		return models.PaymentStatusPaid
	default:
		return models.PaymentStatusPartial
	}
}
