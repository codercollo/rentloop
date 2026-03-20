// Package bot provides the database access layer for the WhatsApp bot interface.
package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository handles all SQL queries the bot needs.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository returns a bot repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetLandlordByPhone returns a landlord by WhatsApp phone number.
func (r *Repository) GetLandlordByPhone(ctx context.Context, phone string) (*models.Landlord, error) {
	var l models.Landlord
	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  whatsapp_phone = $1
		LIMIT  1
	`, phone).Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name, &l.PaybillNumber,
		&l.SubscriptionStatus, &l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get landlord by phone: %w", err)
	}
	return &l, nil
}

// GetAgentByPhone returns an agent by WhatsApp phone number.
func (r *Repository) GetAgentByPhone(ctx context.Context, phone string) (*models.Agent, error) {
	var a models.Agent
	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name, unit_count, created_at
		FROM   agents
		WHERE  whatsapp_phone = $1
		LIMIT  1
	`, phone).Scan(
		&a.ID, &a.WhatsAppPhone, &a.Name, &a.UnitCount, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get agent by phone: %w", err)
	}
	return &a, nil
}

// UnitStatus holds a unit with its current month payment state.
type UnitStatus struct {
	Unit      models.Unit
	TotalPaid int
	IsPaid    bool
}

// GetUnitsWithStatus returns all active units for a landlord with
// their payment status for the current month.
func (r *Repository) GetUnitsWithStatus(ctx context.Context, landlordID, monthKey string) ([]UnitStatus, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			u.id, u.landlord_id, u.unit_ref, u.tenant_name,
			u.tenant_phone, u.expected_rent, u.active, u.effective_from, u.created_at,
			COALESCE(SUM(p.amount) FILTER (WHERE p.status != 'unmatched'), 0) AS total_paid
		FROM   units u
		LEFT   JOIN payments p
			ON p.unit_id    = u.id
			AND p.month_key = $2
		WHERE  u.landlord_id = $1
		  AND  u.active      = TRUE
		GROUP  BY u.id
		ORDER  BY u.unit_ref
	`, landlordID, monthKey)
	if err != nil {
		return nil, fmt.Errorf("get units with status: %w", err)
	}
	defer rows.Close()

	var results []UnitStatus
	for rows.Next() {
		var us UnitStatus
		if err := rows.Scan(
			&us.Unit.ID, &us.Unit.LandlordID, &us.Unit.UnitRef, &us.Unit.TenantName,
			&us.Unit.TenantPhone, &us.Unit.ExpectedRent, &us.Unit.Active,
			&us.Unit.EffectiveFrom, &us.Unit.CreatedAt,
			&us.TotalPaid,
		); err != nil {
			return nil, fmt.Errorf("scan unit status: %w", err)
		}
		us.IsPaid = us.TotalPaid >= us.Unit.ExpectedRent
		results = append(results, us)
	}
	return results, rows.Err()
}

// GetLandlordsByAgent returns all landlords managed by an agent.
func (r *Repository) GetLandlordsByAgent(ctx context.Context, agentID string) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  agent_id = $1
		ORDER  BY name
	`, agentID)
	if err != nil {
		return nil, fmt.Errorf("get landlords by agent: %w", err)
	}
	defer rows.Close()

	var results []models.Landlord
	for rows.Next() {
		var l models.Landlord
		if err := rows.Scan(
			&l.ID, &l.WhatsAppPhone, &l.Name, &l.PaybillNumber,
			&l.SubscriptionStatus, &l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan landlord: %w", err)
		}
		results = append(results, l)
	}
	return results, rows.Err()
}

// GetLandlordByAgentAndName finds a landlord by partial name within an agent's portfolio.
func (r *Repository) GetLandlordByAgentAndName(ctx context.Context, agentID, name string) (*models.Landlord, error) {
	var l models.Landlord
	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  agent_id = $1
		  AND  LOWER(name) LIKE LOWER($2)
		LIMIT  1
	`, agentID, "%"+name+"%").Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name, &l.PaybillNumber,
		&l.SubscriptionStatus, &l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get landlord by name: %w", err)
	}
	return &l, nil
}

// PaymentHistoryRow holds one month of payment data for a unit.
type PaymentHistoryRow struct {
	MonthKey string
	Amount   int
	Status   string
	PaidAt   time.Time
}

// GetPaymentHistory returns the last N months of payments for a unit.
func (r *Repository) GetPaymentHistory(ctx context.Context, unitID string, months int) ([]PaymentHistoryRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT month_key, COALESCE(SUM(amount),0), MAX(status), MAX(paid_at)
		FROM   payments
		WHERE  unit_id = $1
		  AND  status != 'unmatched'
		GROUP  BY month_key
		ORDER  BY month_key DESC
		LIMIT  $2
	`, unitID, months)
	if err != nil {
		return nil, fmt.Errorf("get payment history: %w", err)
	}
	defer rows.Close()

	var results []PaymentHistoryRow
	for rows.Next() {
		var row PaymentHistoryRow
		if err := rows.Scan(&row.MonthKey, &row.Amount, &row.Status, &row.PaidAt); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// GetUnitByRef returns an active unit by landlord + normalised ref.
func (r *Repository) GetUnitByRef(ctx context.Context, landlordID, ref string) (*models.Unit, error) {
	var u models.Unit
	err := r.db.QueryRow(ctx, `
		SELECT id, landlord_id, unit_ref, tenant_name,
		       tenant_phone, expected_rent, active, effective_from, created_at
		FROM   units
		WHERE  landlord_id = $1
		  AND  unit_ref    = $2
		  AND  active      = TRUE
		LIMIT  1
	`, landlordID, ref).Scan(
		&u.ID, &u.LandlordID, &u.UnitRef, &u.TenantName,
		&u.TenantPhone, &u.ExpectedRent, &u.Active, &u.EffectiveFrom, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get unit by ref: %w", err)
	}
	return &u, nil
}

// GetUnmatchedPayment returns an unmatched payment by transaction ID.
func (r *Repository) GetUnmatchedPayment(ctx context.Context, landlordID, transactionID string) (*models.Payment, error) {
	var p models.Payment
	err := r.db.QueryRow(ctx, `
		SELECT id, transaction_id, unit_id, landlord_id,
		       tenant_phone, amount, status, month_key, receipt_url, paid_at
		FROM   payments
		WHERE  landlord_id    = $1
		  AND  transaction_id = $2
		  AND  status         = 'unmatched'
		LIMIT  1
	`, landlordID, transactionID).Scan(
		&p.ID, &p.TransactionID, &p.UnitID, &p.LandlordID,
		&p.TenantPhone, &p.Amount, &p.Status, &p.MonthKey, &p.ReceiptURL, &p.PaidAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get unmatched payment: %w", err)
	}
	return &p, nil
}

// AssignPaymentToUnit updates an unmatched payment to a specific unit.
func (r *Repository) AssignPaymentToUnit(ctx context.Context, paymentID, unitID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE payments SET unit_id = $1, status = 'paid' WHERE id = $2
	`, unitID, paymentID)
	if err != nil {
		return fmt.Errorf("assign payment to unit: %w", err)
	}
	return nil
}

// InsertUnit inserts a new unit.
// ON CONFLICT DO NOTHING means duplicate unit refs are silently skipped.
// Unit count is incremented in the same transaction.
func (r *Repository) InsertUnit(ctx context.Context, u models.Unit) (*models.Unit, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	var inserted models.Unit
	err = tx.QueryRow(ctx, `
		INSERT INTO units (landlord_id, unit_ref, tenant_name, tenant_phone, expected_rent)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (landlord_id, unit_ref) DO NOTHING
		RETURNING id, landlord_id, unit_ref, tenant_name, tenant_phone,
		          expected_rent, active, effective_from, created_at
	`, u.LandlordID, u.UnitRef, u.TenantName, u.TenantPhone, u.ExpectedRent,
	).Scan(
		&inserted.ID, &inserted.LandlordID, &inserted.UnitRef, &inserted.TenantName,
		&inserted.TenantPhone, &inserted.ExpectedRent, &inserted.Active,
		&inserted.EffectiveFrom, &inserted.CreatedAt,
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("insert unit: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE landlords SET unit_count = unit_count + 1 WHERE id = $1`,
		u.LandlordID,
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("update unit count: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &inserted, nil
}

// CreateLandlord inserts a new landlord. On conflict updates the name.
// Satisfies onboarding.Repository.
func (r *Repository) CreateLandlord(ctx context.Context, l models.Landlord) (*models.Landlord, error) {
	var created models.Landlord
	err := r.db.QueryRow(ctx, `
		INSERT INTO landlords (whatsapp_phone, name, paybill_number, subscription_status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (whatsapp_phone) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, whatsapp_phone, name, paybill_number,
		          subscription_status, billing_cycle_end, unit_count, created_at
	`,
		l.WhatsAppPhone,
		l.Name,
		l.PaybillNumber,
		string(l.SubscriptionStatus),
	).Scan(
		&created.ID, &created.WhatsAppPhone, &created.Name, &created.PaybillNumber,
		&created.SubscriptionStatus, &created.BillingCycleEnd, &created.UnitCount, &created.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create landlord: %w", err)
	}
	return &created, nil
}

// UpdateUnitCount increments the landlord unit_count by delta.
// Satisfies onboarding.Repository.
func (r *Repository) UpdateUnitCount(ctx context.Context, landlordID string, delta int) error {
	_, err := r.db.Exec(ctx,
		`UPDATE landlords SET unit_count = unit_count + $1 WHERE id = $2`,
		delta, landlordID,
	)
	if err != nil {
		return fmt.Errorf("update unit count: %w", err)
	}
	return nil
}

// GetLandlordsWithUnpaid returns landlords with unpaid units this month.
// Used by the 6 PM digest cron job.
func (r *Repository) GetLandlordsWithUnpaid(ctx context.Context, monthKey string) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT l.id, l.whatsapp_phone, l.name, l.paybill_number,
		       l.subscription_status, l.billing_cycle_end, l.unit_count, l.created_at
		FROM   landlords l
		JOIN   units u ON u.landlord_id = l.id AND u.active = TRUE
		WHERE  l.subscription_status IN ('active','grace')
		  AND  NOT EXISTS (
			SELECT 1 FROM payments p
			WHERE p.unit_id   = u.id
			  AND p.month_key = $1
			  AND p.status   != 'unmatched'
			  AND p.amount   >= u.expected_rent
		  )
	`, monthKey)
	if err != nil {
		return nil, fmt.Errorf("get landlords with unpaid: %w", err)
	}
	defer rows.Close()

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

func (r *Repository) UpdateExpectedRent(ctx context.Context, landlordID, unitRef string, rent int) error {
	tag, err := r.db.Exec(ctx, `
        UPDATE units SET expected_rent = $1
        WHERE  landlord_id = $2 AND unit_ref = $3 AND active = TRUE
    `, rent, landlordID, unitRef)
	if err != nil {
		return fmt.Errorf("update expected rent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return models.ErrNotFound
	}
	return nil
}

func (r *Repository) ReplaceUnitTenant(ctx context.Context, landlordID string, u models.Unit) (*models.Unit, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE units SET active = FALSE WHERE landlord_id = $1 AND unit_ref = $2 AND active = TRUE`,
		landlordID, u.UnitRef)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("deactivate unit: %w", err)
	}
	var inserted models.Unit
	err = tx.QueryRow(ctx, `
        INSERT INTO units (landlord_id, unit_ref, tenant_name, tenant_phone, expected_rent)
        VALUES ($1,$2,$3,$4,$5)
        RETURNING id, landlord_id, unit_ref, tenant_name, tenant_phone,
                  expected_rent, active, effective_from, created_at
    `, landlordID, u.UnitRef, u.TenantName, u.TenantPhone, u.ExpectedRent,
	).Scan(&inserted.ID, &inserted.LandlordID, &inserted.UnitRef, &inserted.TenantName,
		&inserted.TenantPhone, &inserted.ExpectedRent, &inserted.Active,
		&inserted.EffectiveFrom, &inserted.CreatedAt)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("insert replacement: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &inserted, nil
}

func (r *Repository) InsertManualPayment(ctx context.Context, p models.Payment) (*models.Payment, error) {
	var inserted models.Payment
	var scannedUnitID *string
	err := r.db.QueryRow(ctx, `
        INSERT INTO payments (transaction_id, unit_id, landlord_id, tenant_phone, amount, status, month_key, receipt_url, paid_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,'',NOW())
        RETURNING id, transaction_id, unit_id, landlord_id, tenant_phone, amount, status, month_key, receipt_url, paid_at
    `, p.TransactionID, p.UnitID, p.LandlordID, p.TenantPhone, p.Amount, string(p.Status), p.MonthKey,
	).Scan(&inserted.ID, &inserted.TransactionID, &scannedUnitID, &inserted.LandlordID,
		&inserted.TenantPhone, &inserted.Amount, &inserted.Status,
		&inserted.MonthKey, &inserted.ReceiptURL, &inserted.PaidAt)
	if err != nil {
		return nil, fmt.Errorf("insert manual payment: %w", err)
	}
	if scannedUnitID != nil {
		inserted.UnitID = *scannedUnitID
	}
	return &inserted, nil
}
