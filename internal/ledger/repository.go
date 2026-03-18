// Package ledger handles all payment persistence and SQL operations.
// The repository layer contains only SQL — no business logic lives here.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository handles all payment SQL operations.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository returns a repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// InsertPayment writes a new payment row and returns the inserted record.
// Returns models.ErrDuplicate when transaction_id already exists —
// this is the idempotency guard against Daraja duplicate callbacks.
func (r *Repository) InsertPayment(ctx context.Context, p models.Payment) (*models.Payment, error) {
	unitID := p.UnitID
	if unitID == "" {
		unitID = "00000000-0000-0000-0000-000000000000"
	}

	var inserted models.Payment
	err := r.db.QueryRow(ctx, `
		INSERT INTO payments (
			transaction_id, unit_id, landlord_id,
			tenant_phone, amount, status,
			month_key, receipt_url, paid_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, transaction_id, unit_id, landlord_id,
		          tenant_phone, amount, status,
		          month_key, receipt_url, paid_at
	`,
		p.TransactionID, unitID, p.LandlordID,
		p.TenantPhone, p.Amount, string(p.Status),
		p.MonthKey, p.ReceiptURL, p.PaidAt,
	).Scan(
		&inserted.ID, &inserted.TransactionID, &inserted.UnitID, &inserted.LandlordID,
		&inserted.TenantPhone, &inserted.Amount, &inserted.Status,
		&inserted.MonthKey, &inserted.ReceiptURL, &inserted.PaidAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, models.ErrDuplicate
		}
		return nil, fmt.Errorf("insert payment: %w", err)
	}

	return &inserted, nil
}

// GetMonthlyTotal returns the sum of all non-unmatched payments
// for a unit in a given month. Returns 0 when no payments exist.
func (r *Repository) GetMonthlyTotal(ctx context.Context, unitID, monthKey string) (int, error) {
	var total int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM   payments
		WHERE  unit_id   = $1
		  AND  month_key = $2
		  AND  status   != 'unmatched'
	`, unitID, monthKey).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("get monthly total: %w", err)
	}
	return total, nil
}

// GetUnit fetches a unit row by ID — used to read expected_rent.
func (r *Repository) GetUnit(ctx context.Context, unitID string) (*models.Unit, error) {
	var u models.Unit
	err := r.db.QueryRow(ctx, `
		SELECT id, landlord_id, unit_ref, tenant_name,
		       tenant_phone, expected_rent, active, effective_from, created_at
		FROM   units
		WHERE  id = $1
	`, unitID).Scan(
		&u.ID, &u.LandlordID, &u.UnitRef, &u.TenantName,
		&u.TenantPhone, &u.ExpectedRent, &u.Active, &u.EffectiveFrom, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get unit: %w", err)
	}
	return &u, nil
}

// GetUnitByRef fetches an active unit by landlord + normalised ref.
// Satisfies the matcher.Repository interface so the matcher package
// can share this repository.
func (r *Repository) GetUnitByRef(ctx context.Context, landlordID, normalisedRef string) (*models.Unit, error) {
	var u models.Unit
	err := r.db.QueryRow(ctx, `
		SELECT id, landlord_id, unit_ref, tenant_name,
		       tenant_phone, expected_rent, active, effective_from, created_at
		FROM   units
		WHERE  landlord_id = $1
		  AND  unit_ref    = $2
		  AND  active      = TRUE
		LIMIT  1
	`, landlordID, normalisedRef).Scan(
		&u.ID, &u.LandlordID, &u.UnitRef, &u.TenantName,
		&u.TenantPhone, &u.ExpectedRent, &u.Active, &u.EffectiveFrom, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNoMatch
		}
		return nil, fmt.Errorf("get unit by ref: %w", err)
	}
	return &u, nil
}

// GetByPaybill fetches a landlord row by paybill number.
// Satisfies the mpesa.LandlordRepository interface.
func (r *Repository) GetByPaybill(ctx context.Context, paybill string) (*models.Landlord, error) {
	var l models.Landlord
	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  paybill_number = $1
		LIMIT  1
	`, paybill).Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name, &l.PaybillNumber,
		&l.SubscriptionStatus, &l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get landlord by paybill: %w", err)
	}
	return &l, nil
}
