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
	var unitID *string
	if p.UnitID != "" {
		unitID = &p.UnitID
	}

	source := p.PaymentSource
	if source == "" {
		source = models.PaymentSourceMpesaSTK
	}

	var inserted models.Payment
	var scannedUnitID *string

	err := r.db.QueryRow(ctx, `
		INSERT INTO payments (
			transaction_id, unit_id, landlord_id,
			tenant_phone, amount, status,
			month_key, receipt_url, paid_at,
			expected_rent_snapshot, arrears_carried, payment_source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING
			id, transaction_id, unit_id, landlord_id,
			tenant_phone, amount, status,
			month_key, receipt_url, paid_at,
			expected_rent_snapshot, arrears_carried, payment_source
	`,
		p.TransactionID, unitID, p.LandlordID,
		p.TenantPhone, p.Amount, string(p.Status),
		p.MonthKey, p.ReceiptURL, p.PaidAt,
		p.ExpectedRentSnapshot, p.ArrearsCarried, string(source),
	).Scan(
		&inserted.ID,
		&inserted.TransactionID,
		&scannedUnitID,
		&inserted.LandlordID,
		&inserted.TenantPhone,
		&inserted.Amount,
		&inserted.Status,
		&inserted.MonthKey,
		&inserted.ReceiptURL,
		&inserted.PaidAt,
		&inserted.ExpectedRentSnapshot,
		&inserted.ArrearsCarried,
		&inserted.PaymentSource,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, models.ErrDuplicate
		}
		return nil, fmt.Errorf("insert payment: %w", err)
	}

	if scannedUnitID != nil {
		inserted.UnitID = *scannedUnitID
	}

	return &inserted, nil
}

// GetMonthlyTotal returns the sum of all non-unmatched payments for a unit
// in the given month_key. Returns 0 when no payments exist.
//
// BUG FIX: The original query had a redundant AND clause:
//
//	AND paid_at >= date_trunc('month', NOW() AT TIME ZONE 'Africa/Nairobi')
//
// This double-filtered by both month_key AND paid_at. A payment recorded
// near midnight at a month boundary could have month_key='2026-03' but
// paid_at technically in the previous UTC day, causing it to be silently
// excluded from the running total. Since month_key is the authoritative
// bucketing field (set explicitly from the M-Pesa TransTime), the paid_at
// clause is redundant and dangerous. Removed.
func (r *Repository) GetMonthlyTotal(ctx context.Context, unitID, monthKey string) (int, error) {
	var total int
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM   payments
		WHERE  unit_id   = $1
		  AND  month_key = $2
		  AND  status   != 'unmatched'
		  AND  status   != 'unknown'
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
// Satisfies the matcher.Repository interface.
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
		SELECT id, whatsapp_phone, name, apartment_name, premise_name,
		       paybill_number, subscription_status, billing_cycle_end,
		       unit_count, created_at
		FROM   landlords
		WHERE  paybill_number = $1
		LIMIT  1
	`, paybill).Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name, &l.ApartmentName, &l.PremiseName,
		&l.PaybillNumber, &l.SubscriptionStatus, &l.BillingCycleEnd,
		&l.UnitCount, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get landlord by paybill: %w", err)
	}
	return &l, nil
}

// GetPaymentHistory12 returns up to 12 months of payment rows for a unit,
// ordered newest-first. Each row is one month — multiple payments in the same
// month are aggregated.
// GetPaymentHistory12 returns up to 12 months of payment history for a unit,
// newest month first. Multiple payments in the same month are aggregated.
// Overpayments are preserved (used for HISTORY-EXT), unmatched payments are excluded.
func (r *Repository) GetPaymentHistory12(ctx context.Context, unitID string) ([]models.MonthlyPaymentRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			month_key,
			SUM(amount)                                 AS amount_paid,
			MAX(expected_rent_snapshot)                 AS expected_rent_snapshot,
			MAX(arrears_carried)                        AS arrears_carried,
			CASE
				WHEN SUM(amount) = 0                              THEN 'unmatched'
				WHEN SUM(amount) >= MAX(expected_rent_snapshot) * 2 THEN 'overpaid'
				WHEN SUM(amount) >= MAX(expected_rent_snapshot)   THEN 'paid'
				ELSE 'partial'
			END                                         AS status,
			MODE() WITHIN GROUP (ORDER BY payment_source) AS payment_source,
			ARRAY_AGG(transaction_id ORDER BY paid_at)  AS receipt_ids,
			MAX(paid_at)                                AS paid_at
		FROM payments
		WHERE unit_id = $1
		  AND status != 'unmatched'
		GROUP BY month_key
		ORDER BY month_key DESC
		LIMIT 12
	`, unitID)
	if err != nil {
		return nil, fmt.Errorf("get payment history 12 query: %w", err)
	}
	defer rows.Close()

	var result []models.MonthlyPaymentRow
	for rows.Next() {
		var row models.MonthlyPaymentRow
		var receiptIDs []string

		if err := rows.Scan(
			&row.MonthKey,
			&row.AmountPaid,
			&row.ExpectedRentSnapshot,
			&row.ArrearsCarried,
			&row.Status,
			&row.Source,
			&receiptIDs,
			&row.PaidAt,
		); err != nil {
			return nil, fmt.Errorf("scan payment history row: %w", err)
		}

		row.ReceiptIDs = receiptIDs
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment history rows: %w", err)
	}

	return result, nil
}

// GetPortfolioHistory12 returns up to 12 months of aggregate performance
// across all units for a landlord, ordered newest-first.
//
// Overpayments are capped at the expected rent per unit so that the
// portfolio totals reflect actual expected revenue, not extra payments.
// Unmatched payments are excluded from the portfolio aggregation.
func (r *Repository) GetPortfolioHistory12(ctx context.Context, landlordID string) ([]models.PortfolioMonthRow, error) {
	rows, err := r.db.Query(ctx, `
WITH unit_months AS (
    SELECT
        p.month_key,
        p.unit_id,
        SUM(p.amount)                 AS amount_paid,
        MAX(p.expected_rent_snapshot) AS expected_rent_snapshot,
        MAX(p.arrears_carried)        AS arrears_carried,
        CASE
            WHEN SUM(p.amount) >= MAX(p.expected_rent_snapshot) THEN 1
            ELSE 0
        END                           AS is_paid
    FROM payments p
    WHERE p.landlord_id = $1
      AND p.status != 'unmatched'
    GROUP BY p.month_key, p.unit_id
)
SELECT
    month_key,
    COALESCE(SUM(expected_rent_snapshot), 0)                      AS total_expected,
    COALESCE(SUM(LEAST(amount_paid, expected_rent_snapshot)), 0)  AS total_collected,
    COALESCE(SUM(arrears_carried), 0)                             AS total_arrears,
    SUM(is_paid)::INT                                             AS units_paid,
    COUNT(*)::INT                                                 AS units_total
FROM unit_months
GROUP BY month_key
ORDER BY month_key DESC
LIMIT 12
	`, landlordID)
	if err != nil {
		return nil, fmt.Errorf("get portfolio history 12 query: %w", err)
	}
	defer rows.Close()

	var result []models.PortfolioMonthRow
	for rows.Next() {
		var row models.PortfolioMonthRow
		if err := rows.Scan(
			&row.MonthKey,
			&row.TotalExpected,
			&row.TotalCollected,
			&row.TotalArrears,
			&row.UnitsPaid,
			&row.UnitsTotal,
		); err != nil {
			return nil, fmt.Errorf("scan portfolio history row: %w", err)
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate portfolio history rows: %w", err)
	}

	return result, nil
}
