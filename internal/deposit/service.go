// Package deposit implements deposit business logic.
//
// One deposits row per unit holds the current balance state.
// Every mutation writes a deposit_transactions row for the audit trail.
// The balance in deposits is always derivable from the transaction log;
// the stored columns are a pre-computed view for fast reads.
package deposit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/codercollo/rentloop/internal/models"
)

// Repo is the persistence interface the service depends on.
// Satisfied by Repository. Tests inject a mock.
type Repo interface {
	GetByUnit(ctx context.Context, unitID string) (*models.Deposit, error)
	Upsert(ctx context.Context, d models.Deposit) (*models.Deposit, error)
	InsertTransaction(ctx context.Context, t models.DepositTransaction) (*models.DepositTransaction, error)
	GetTransactions(ctx context.Context, depositID string, limit int) ([]models.DepositTransaction, error)
}

// UnitFetcher resolves a unit ref to a unit row.
// Satisfied by the ledger repository.
type UnitFetcher interface {
	GetUnitByRef(ctx context.Context, landlordID, normalisedRef string) (*models.Unit, error)
}

// Service handles deposit business logic.
type Service struct {
	repo        Repo
	unitFetcher UnitFetcher
}

// NewService wires dependencies.
func NewService(repo Repo, unitFetcher UnitFetcher) *Service {
	return &Service{repo: repo, unitFetcher: unitFetcher}
}

// ── Public operations ─────────────────────────────────────────────────────────

// DepositResult is the return value from Get, Receive, and Refund.
type DepositResult struct {
	Deposit      *models.Deposit
	Transactions []models.DepositTransaction
	Unit         *models.Unit
}

// Get returns the current deposit state and the last 10 audit entries.
// If no deposit record exists yet, returns a zeroed Deposit (not an error).
func (s *Service) Get(ctx context.Context, landlordID, normalisedRef string) (*DepositResult, error) {
	unit, err := s.unitFetcher.GetUnitByRef(ctx, landlordID, normalisedRef)
	if err != nil {
		return nil, fmt.Errorf("deposit get: resolve unit: %w", err)
	}

	dep, err := s.repo.GetByUnit(ctx, unit.ID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			// No deposit recorded yet — return a zeroed record so the bot
			// can display "No deposit on record" without crashing.
			return &DepositResult{
				Deposit: &models.Deposit{
					UnitID:     unit.ID,
					LandlordID: landlordID,
				},
				Unit: unit,
			}, nil
		}
		return nil, fmt.Errorf("deposit get: fetch: %w", err)
	}

	txns, err := s.repo.GetTransactions(ctx, dep.ID, 10)
	if err != nil {
		return nil, fmt.Errorf("deposit get: fetch transactions: %w", err)
	}

	return &DepositResult{Deposit: dep, Transactions: txns, Unit: unit}, nil
}

// Receive records an incoming deposit payment.
// If no deposit row exists for the unit, one is created with deposit_expected
// defaulting to the payment amount (landlord can adjust later).
//
// recordedBy should be 'landlord' or 'agent'.
func (s *Service) Receive(ctx context.Context, landlordID, normalisedRef string, amount int, note, recordedBy string) (*DepositResult, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("deposit receive: amount must be positive")
	}

	unit, err := s.unitFetcher.GetUnitByRef(ctx, landlordID, normalisedRef)
	if err != nil {
		return nil, fmt.Errorf("deposit receive: resolve unit: %w", err)
	}

	// Load or initialise the deposit state row.
	dep, err := s.repo.GetByUnit(ctx, unit.ID)
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			return nil, fmt.Errorf("deposit receive: fetch: %w", err)
		}
		// First deposit for this unit — seed expected = amount received.
		dep = &models.Deposit{
			UnitID:          unit.ID,
			LandlordID:      landlordID,
			DepositExpected: amount,
		}
	}

	dep.DepositPaid += amount
	// DepositBalance = amount still owed by tenant (outstanding, not yet paid).
	// This is separate from "held" (paid − refunded) which is computed on read.
	dep.DepositBalance = max(0, dep.DepositExpected-dep.DepositPaid)

	saved, err := s.repo.Upsert(ctx, *dep)
	if err != nil {
		return nil, fmt.Errorf("deposit receive: upsert: %w", err)
	}

	txn, err := s.repo.InsertTransaction(ctx, models.DepositTransaction{
		DepositID:  saved.ID,
		UnitID:     unit.ID,
		LandlordID: landlordID,
		TxnType:    models.DepositTxnReceived,
		Amount:     amount,
		Note:       note,
		RecordedBy: recordedBy,
	})
	if err != nil {
		// Non-fatal: balance already updated. Log and continue.
		slog.Error("deposit receive: insert transaction failed",
			"deposit_id", saved.ID, "error", err)
	}

	slog.Info("deposit: received",
		"unit", unit.UnitRef,
		"amount", amount,
		"balance", saved.DepositBalance,
	)

	var txns []models.DepositTransaction
	if txn != nil {
		txns = []models.DepositTransaction{*txn}
	}
	return &DepositResult{Deposit: saved, Transactions: txns, Unit: unit}, nil
}

// Refund records a deposit refund.
// Returns an error if the refund amount exceeds what was paid minus
// what has already been refunded.
//
// BUG FIX: The original Refund had:
//
//	dep.DepositBalance = max(0, dep.DepositExpected-dep.DepositPaid)
//
// DepositBalance represents "outstanding deposit owed by the tenant" (i.e.
// how much more they still need to pay towards the expected deposit). This
// value should not change when a refund is issued — a refund does not affect
// what the tenant owes; it only affects what the landlord holds.
//
// The "held" amount (DepositPaid − DepositRefunded) is computed on read in
// depositStatusSummary and cmdDeposit, so it is always consistent. The only
// stored value that changes during a refund is DepositRefunded.
//
// Leaving DepositBalance unchanged on refund is correct. The line was wrong
// because it re-assigned DepositBalance to the same value it already had
// (since DepositPaid didn't change), which was harmless but misleading.
// Removed to make intent explicit.
func (s *Service) Refund(ctx context.Context, landlordID, normalisedRef string, amount int, note, recordedBy string) (*DepositResult, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("deposit refund: amount must be positive")
	}

	unit, err := s.unitFetcher.GetUnitByRef(ctx, landlordID, normalisedRef)
	if err != nil {
		return nil, fmt.Errorf("deposit refund: resolve unit: %w", err)
	}

	dep, err := s.repo.GetByUnit(ctx, unit.ID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return nil, fmt.Errorf("deposit refund: no deposit record exists for unit %s", unit.UnitRef)
		}
		return nil, fmt.Errorf("deposit refund: fetch: %w", err)
	}

	// Guard: cannot refund more than was paid minus prior refunds.
	refundable := dep.DepositPaid - dep.DepositRefunded
	if amount > refundable {
		return nil, fmt.Errorf(
			"deposit refund: amount KES %d exceeds refundable balance KES %d",
			amount, refundable,
		)
	}

	dep.DepositRefunded += amount
	// DepositBalance (tenant outstanding) is unaffected by a refund —
	// it only reflects expected vs paid. No assignment needed here.

	saved, err := s.repo.Upsert(ctx, *dep)
	if err != nil {
		return nil, fmt.Errorf("deposit refund: upsert: %w", err)
	}

	txn, err := s.repo.InsertTransaction(ctx, models.DepositTransaction{
		DepositID:  saved.ID,
		UnitID:     unit.ID,
		LandlordID: landlordID,
		TxnType:    models.DepositTxnRefund,
		Amount:     amount,
		Note:       note,
		RecordedBy: recordedBy,
	})
	if err != nil {
		slog.Error("deposit refund: insert transaction failed",
			"deposit_id", saved.ID, "error", err)
	}

	slog.Info("deposit: refunded",
		"unit", unit.UnitRef,
		"amount", amount,
		"refunded_total", saved.DepositRefunded,
	)

	var txns []models.DepositTransaction
	if txn != nil {
		txns = []models.DepositTransaction{*txn}
	}
	return &DepositResult{Deposit: saved, Transactions: txns, Unit: unit}, nil
}

// max is a local helper for Go versions before 1.21.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
