// Package billing manages subscription lifecycle for RentLoop landlords.
package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository handles all billing SQL.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository returns a billing repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetLandlordByID fetches a landlord by ID.
func (r *Repository) GetLandlordByID(ctx context.Context, id string) (*models.Landlord, error) {
	var l models.Landlord
	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords WHERE id = $1
	`, id).Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name, &l.PaybillNumber,
		&l.SubscriptionStatus, &l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get landlord by id: %w", err)
	}
	return &l, nil
}

// GetExpiredPaidAccounts returns active paid accounts whose cycle has ended.
func (r *Repository) GetExpiredPaidAccounts(ctx context.Context) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  subscription_status = 'active'
		  AND  unit_count          > $1
		  AND  billing_cycle_end   < NOW()
	`, 0)
	if err != nil {
		return nil, fmt.Errorf("get expired accounts: %w", err)
	}
	defer rows.Close()
	return scanLandlords(rows)
}

// GetGraceAccounts returns accounts in grace period past the grace deadline.
func (r *Repository) GetGraceAccounts(ctx context.Context, graceDays int) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  subscription_status = 'grace'
		  AND  billing_cycle_end   < NOW() - ($1 || ' days')::interval
	`, graceDays)
	if err != nil {
		return nil, fmt.Errorf("get grace accounts: %w", err)
	}
	defer rows.Close()
	return scanLandlords(rows)
}

// SetStatus updates subscription_status for a landlord.
func (r *Repository) SetStatus(ctx context.Context, landlordID string, status models.SubscriptionStatus) error {
	_, err := r.db.Exec(ctx,
		`UPDATE landlords SET subscription_status = $1 WHERE id = $2`,
		string(status), landlordID,
	)
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	return nil
}

// Activate sets status to active and extends billing_cycle_end by 30 days.
func (r *Repository) Activate(ctx context.Context, landlordID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE landlords
		SET    subscription_status = 'active',
		       billing_cycle_end   = NOW() + INTERVAL '30 days'
		WHERE  id = $1
	`, landlordID)
	if err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	return nil
}

// RecordPayment inserts a subscription payment row.
func (r *Repository) RecordPayment(ctx context.Context, p models.SubscriptionPayment) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO subscription_payments
		       (landlord_id, transaction_id, amount, paid_at, period_start, period_end)
		VALUES ($1,$2,$3,NOW(),$4,$5)
		ON CONFLICT (transaction_id) DO NOTHING
	`,
		p.LandlordID, p.TransactionID, p.Amount,
		p.PeriodStart, p.PeriodEnd,
	)
	if err != nil {
		return fmt.Errorf("record subscription payment: %w", err)
	}
	return nil
}

// GetLandlordByRef finds a landlord whose subscription ref matches RENTLOOP-{id_prefix}.
func (r *Repository) GetLandlordByRef(ctx context.Context, ref string) (*models.Landlord, error) {
	// ref format: RENTLOOP-{first 8 chars of landlord UUID}
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  LEFT(id::text, 8) = RIGHT($1, 8)
		LIMIT  1
	`, ref)
	if err != nil {
		return nil, fmt.Errorf("get landlord by ref: %w", err)
	}
	defer rows.Close()

	landlords, err := scanLandlords(rows)
	if err != nil {
		return nil, err
	}
	if len(landlords) == 0 {
		return nil, models.ErrNotFound
	}
	return &landlords[0], nil
}

func scanLandlords(rows pgx.Rows) ([]models.Landlord, error) {
	var results []models.Landlord
	for rows.Next() {
		var l models.Landlord
		if err := rows.Scan(
			&l.ID, &l.WhatsAppPhone, &l.Name, &l.PaybillNumber,
			&l.SubscriptionStatus, &l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, l)
	}
	return results, rows.Err()
}

// MRRRow holds one row of MRR data for the admin dashboard.
type MRRRow struct {
	Month         string
	Revenue       int
	LandlordCount int
}

// GetMRR returns the last 6 months of subscription revenue.
func (r *Repository) GetMRR(ctx context.Context) ([]MRRRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT TO_CHAR(paid_at, 'YYYY-MM') AS month,
		       SUM(amount)                 AS revenue,
		       COUNT(DISTINCT landlord_id) AS landlord_count
		FROM   subscription_payments
		WHERE  paid_at > NOW() - INTERVAL '6 months'
		GROUP  BY month
		ORDER  BY month DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get mrr: %w", err)
	}
	defer rows.Close()

	var results []MRRRow
	for rows.Next() {
		var m MRRRow
		if err := rows.Scan(&m.Month, &m.Revenue, &m.LandlordCount); err != nil {
			return nil, err
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

// GetSubscriptionPayments returns recent subscription payments for a landlord.
func (r *Repository) GetSubscriptionPayments(ctx context.Context, landlordID string) ([]models.SubscriptionPayment, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, landlord_id, transaction_id, amount, paid_at, period_start, period_end
		FROM   subscription_payments
		WHERE  landlord_id = $1
		ORDER  BY paid_at DESC
		LIMIT  12
	`, landlordID)
	if err != nil {
		return nil, fmt.Errorf("get subscription payments: %w", err)
	}
	defer rows.Close()

	var results []models.SubscriptionPayment
	for rows.Next() {
		var p models.SubscriptionPayment
		if err := rows.Scan(
			&p.ID, &p.LandlordID, &p.TransactionID,
			&p.Amount, &p.PaidAt, &p.PeriodStart, &p.PeriodEnd,
		); err != nil {
			return nil, err
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

// GetAllLandlords returns all landlords for status transition cron.
func (r *Repository) GetAllActive(ctx context.Context) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  subscription_status IN ('active','grace')
		  AND  unit_count > 0
	`)
	if err != nil {
		return nil, fmt.Errorf("get all active: %w", err)
	}
	defer rows.Close()
	return scanLandlords(rows)
}

// FreeTierLimit is the unit count below which billing never runs.
// Matches FREE_TIER_UNIT_LIMIT in config.
var _ = time.Now // keep time import
