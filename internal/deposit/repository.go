// Package deposits manages security deposit tracking and the full audit
// trail of deposit transactions per unit.
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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository handles all deposit SQL operations.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository returns a repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ── deposits table ────────────────────────────────────────────────────────────

// GetByUnit fetches the current deposit state for a unit.
// Returns models.ErrNotFound when no deposit record exists yet.
func (r *Repository) GetByUnit(ctx context.Context, unitID string) (*models.Deposit, error) {
	var d models.Deposit
	err := r.db.QueryRow(ctx, `
		SELECT id, unit_id, landlord_id,
		       deposit_expected, deposit_paid, deposit_balance, deposit_refunded,
		       created_at, updated_at
		FROM   deposits
		WHERE  unit_id = $1
	`, unitID).Scan(
		&d.ID, &d.UnitID, &d.LandlordID,
		&d.DepositExpected, &d.DepositPaid, &d.DepositBalance, &d.DepositRefunded,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get deposit by unit: %w", err)
	}
	return &d, nil
}

// Upsert creates or updates the deposit state row for a unit.
// ON CONFLICT updates all mutable columns atomically.
func (r *Repository) Upsert(ctx context.Context, d models.Deposit) (*models.Deposit, error) {
	var out models.Deposit
	err := r.db.QueryRow(ctx, `
		INSERT INTO deposits (
			unit_id, landlord_id,
			deposit_expected, deposit_paid, deposit_balance, deposit_refunded,
			updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (unit_id) DO UPDATE SET
			deposit_expected  = EXCLUDED.deposit_expected,
			deposit_paid      = EXCLUDED.deposit_paid,
			deposit_balance   = EXCLUDED.deposit_balance,
			deposit_refunded  = EXCLUDED.deposit_refunded,
			updated_at        = NOW()
		RETURNING
			id, unit_id, landlord_id,
			deposit_expected, deposit_paid, deposit_balance, deposit_refunded,
			created_at, updated_at
	`,
		d.UnitID, d.LandlordID,
		d.DepositExpected, d.DepositPaid, d.DepositBalance, d.DepositRefunded,
	).Scan(
		&out.ID, &out.UnitID, &out.LandlordID,
		&out.DepositExpected, &out.DepositPaid, &out.DepositBalance, &out.DepositRefunded,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert deposit: %w", err)
	}
	return &out, nil
}

// ── deposit_transactions table ────────────────────────────────────────────────

// InsertTransaction appends one audit entry for a deposit event.
func (r *Repository) InsertTransaction(ctx context.Context, t models.DepositTransaction) (*models.DepositTransaction, error) {
	if t.RecordedAt.IsZero() {
		t.RecordedAt = time.Now()
	}
	if t.RecordedBy == "" {
		t.RecordedBy = "landlord"
	}

	var out models.DepositTransaction
	err := r.db.QueryRow(ctx, `
		INSERT INTO deposit_transactions (
			deposit_id, unit_id, landlord_id,
			txn_type, amount, note,
			recorded_at, recorded_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING
			id, deposit_id, unit_id, landlord_id,
			txn_type, amount, note,
			recorded_at, recorded_by
	`,
		t.DepositID, t.UnitID, t.LandlordID,
		string(t.TxnType), t.Amount, t.Note,
		t.RecordedAt, t.RecordedBy,
	).Scan(
		&out.ID, &out.DepositID, &out.UnitID, &out.LandlordID,
		&out.TxnType, &out.Amount, &out.Note,
		&out.RecordedAt, &out.RecordedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("insert deposit transaction: %w", err)
	}
	return &out, nil
}

// GetTransactions returns the audit trail for a deposit, newest-first.
// Limit controls how many rows to return (0 = no limit).
func (r *Repository) GetTransactions(ctx context.Context, depositID string, limit int) ([]models.DepositTransaction, error) {
	q := `
		SELECT id, deposit_id, unit_id, landlord_id,
		       txn_type, amount, note,
		       recorded_at, recorded_by
		FROM   deposit_transactions
		WHERE  deposit_id = $1
		ORDER  BY recorded_at DESC
	`
	args := []any{depositID}
	if limit > 0 {
		q += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("get deposit transactions: %w", err)
	}
	defer rows.Close()

	var result []models.DepositTransaction
	for rows.Next() {
		var t models.DepositTransaction
		if err := rows.Scan(
			&t.ID, &t.DepositID, &t.UnitID, &t.LandlordID,
			&t.TxnType, &t.Amount, &t.Note,
			&t.RecordedAt, &t.RecordedBy,
		); err != nil {
			return nil, fmt.Errorf("scan deposit transaction: %w", err)
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deposit transactions: %w", err)
	}
	return result, nil
}
