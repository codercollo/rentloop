// Package bot provides the database access layer for the WhatsApp bot interface.
package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// ── Landlord / agent resolution ───────────────────────────────────────────────

// GetLandlordByPhone returns a landlord by WhatsApp phone number.
func (r *Repository) GetLandlordByPhone(ctx context.Context, phone string) (*models.Landlord, error) {
	var l models.Landlord
	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name,
		       apartment_name, premise_name,
		       paybill_number, subscription_status,
		       billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  whatsapp_phone = $1
		LIMIT  1
	`, phone).Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name,
		&l.ApartmentName, &l.PremiseName,
		&l.PaybillNumber, &l.SubscriptionStatus,
		&l.BillingCycleEnd, &l.UnitCount, &l.CreatedAt,
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

// ── Units ─────────────────────────────────────────────────────────────────────

// UnitStatus holds a unit with its current month payment state.
type UnitStatus struct {
	Unit      models.Unit
	TotalPaid int
	IsPaid    bool
}

// GetUnitsWithStatus returns all active units for a landlord with their
// payment totals for the given month_key.
func (r *Repository) GetUnitsWithStatus(
	ctx context.Context,
	landlordID string,
	monthKey string,
) ([]UnitStatus, error) {

	rows, err := r.db.Query(ctx, `
		SELECT
		    u.id,
		    u.landlord_id,
		    u.unit_ref,
		    u.tenant_name,
		    u.tenant_phone,
		    u.expected_rent,
		    u.active,
		    u.effective_from,
		    u.created_at,
		    COALESCE(SUM(p.amount), 0)                    AS total_paid,
		    COALESCE(SUM(p.amount), 0) >= u.expected_rent AS is_paid
		FROM  units u
		LEFT  JOIN payments p
		    ON  p.unit_id   = u.id
		    AND p.month_key = $2
		    AND p.status   != 'unmatched'
		WHERE u.landlord_id = $1
		  AND u.active      = TRUE
		GROUP BY
		    u.id, u.landlord_id, u.unit_ref, u.tenant_name,
		    u.tenant_phone, u.expected_rent, u.active,
		    u.effective_from, u.created_at
		ORDER BY u.unit_ref
	`, landlordID, monthKey)
	if err != nil {
		return nil, fmt.Errorf("get units with status: %w", err)
	}
	defer rows.Close()

	var results []UnitStatus
	for rows.Next() {
		var us UnitStatus
		if err := rows.Scan(
			&us.Unit.ID,
			&us.Unit.LandlordID,
			&us.Unit.UnitRef,
			&us.Unit.TenantName,
			&us.Unit.TenantPhone,
			&us.Unit.ExpectedRent,
			&us.Unit.Active,
			&us.Unit.EffectiveFrom,
			&us.Unit.CreatedAt,
			&us.TotalPaid,
			&us.IsPaid,
		); err != nil {
			return nil, fmt.Errorf("scan unit status: %w", err)
		}
		results = append(results, us)
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

// ── Payments ──────────────────────────────────────────────────────────────────

// PaymentHistoryRow holds one aggregated month of payment data for a unit.
type PaymentHistoryRow struct {
	MonthKey string
	Amount   int
	Status   string
	PaidAt   time.Time
}

// GetPaymentHistory returns the last N months of payments for a unit.
func (r *Repository) GetPaymentHistory(ctx context.Context, unitID string, months int) ([]PaymentHistoryRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT month_key, COALESCE(SUM(amount), 0), MAX(status), MAX(paid_at)
		FROM   payments
		WHERE  unit_id  = $1
		  AND  status  != 'unmatched'
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

// GetPaymentHistory12 returns up to 12 months of real payment history for a
// unit, ordered newest-first.
func (r *Repository) GetPaymentHistory12(ctx context.Context, unitID string) ([]models.MonthlyPaymentRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
		    month_key,
		    SUM(amount)::int                                     AS amount_paid,
		    MAX(COALESCE(expected_rent_snapshot, 0))::int        AS expected_rent_snapshot,
		    MAX(COALESCE(arrears_carried, 0))::int               AS arrears_carried,
		    CASE
		        WHEN SUM(amount) = 0                                      THEN 'unmatched'
		        WHEN SUM(amount) >= MAX(expected_rent_snapshot) * 2       THEN 'overpaid'
		        WHEN SUM(amount) >= MAX(expected_rent_snapshot)           THEN 'paid'
		        ELSE                                                            'partial'
		    END                                                  AS status,
		    CASE
		        WHEN bool_or(payment_source = 'bank_paybill') THEN 'bank_paybill'
		        WHEN bool_or(payment_source = 'manual')       THEN 'manual'
		        ELSE                                               'mpesa_stk'
		    END                                                  AS payment_source,
		    ARRAY_AGG(transaction_id ORDER BY paid_at)           AS receipt_ids
		FROM   payments
		WHERE  unit_id  = $1
		  AND  status  != 'unmatched'
		GROUP  BY month_key
		ORDER  BY month_key DESC
		LIMIT  12
	`, unitID)
	if err != nil {
		return nil, fmt.Errorf("get payment history 12: %w", err)
	}
	defer rows.Close()

	var results []models.MonthlyPaymentRow
	for rows.Next() {
		var row models.MonthlyPaymentRow
		var status, source string
		var receiptIDs []string
		if err := rows.Scan(
			&row.MonthKey,
			&row.AmountPaid,
			&row.ExpectedRentSnapshot,
			&row.ArrearsCarried,
			&status,
			&source,
			&receiptIDs,
		); err != nil {
			return nil, fmt.Errorf("scan monthly payment row: %w", err)
		}
		row.Status = models.PaymentStatus(status)
		row.Source = models.PaymentSource(source)
		row.ReceiptIDs = receiptIDs
		results = append(results, row)
	}
	return results, rows.Err()
}

// GetPortfolioHistory12 returns 12 months of portfolio-level collection
// performance for a landlord.
func (r *Repository) GetPortfolioHistory12(ctx context.Context, landlordID string) ([]models.PortfolioMonthRow, error) {
	rows, err := r.db.Query(ctx, `
		WITH unit_months AS (
		    SELECT
		        p.month_key,
		        p.unit_id,
		        SUM(p.amount)                        AS amount_paid,
		        MAX(p.expected_rent_snapshot)        AS expected_rent_snapshot,
		        CASE
		            WHEN SUM(p.amount) >= MAX(p.expected_rent_snapshot) THEN 1
		            ELSE 0
		        END                                  AS is_paid
		    FROM   payments p
		    WHERE  p.landlord_id = $1
		      AND  p.status     != 'unmatched'
		    GROUP  BY p.month_key, p.unit_id
		)
		SELECT
		    month_key,
		    COALESCE(SUM(expected_rent_snapshot), 0)::int        AS total_expected,
		    COALESCE(SUM(amount_paid), 0)::int                   AS total_collected,
		    GREATEST(0,
		        SUM(expected_rent_snapshot - amount_paid)
		            FILTER (WHERE amount_paid < expected_rent_snapshot)
		    )::int                                               AS total_arrears,
		    SUM(is_paid)::int                                    AS units_paid,
		    COUNT(*)::int                                        AS units_total
		FROM   unit_months
		GROUP  BY month_key
		ORDER  BY month_key DESC
		LIMIT  12
	`, landlordID)
	if err != nil {
		return nil, fmt.Errorf("get portfolio history 12: %w", err)
	}
	defer rows.Close()

	var results []models.PortfolioMonthRow
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
			return nil, fmt.Errorf("scan portfolio month row: %w", err)
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// ── Misc unit / payment operations ───────────────────────────────────────────

// GetUnmatchedPayment returns an unmatched payment by transaction ID.
func (r *Repository) GetUnmatchedPayment(ctx context.Context, landlordID, transactionID string) (*models.Payment, error) {
	var p models.Payment
	var unitID *string
	err := r.db.QueryRow(ctx, `
		SELECT id, transaction_id, unit_id, landlord_id,
		       tenant_phone, amount, status, month_key, receipt_url, paid_at
		FROM   payments
		WHERE  landlord_id    = $1
		  AND  transaction_id = $2
		  AND  status         = 'unmatched'
		LIMIT  1
	`, landlordID, transactionID).Scan(
		&p.ID, &p.TransactionID, &unitID, &p.LandlordID,
		&p.TenantPhone, &p.Amount, &p.Status, &p.MonthKey, &p.ReceiptURL, &p.PaidAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get unmatched payment: %w", err)
	}
	if unitID != nil {
		p.UnitID = *unitID
	}
	return &p, nil
}

// AssignPaymentToUnit re-assigns an unmatched payment to a unit and sets the
// correct status based on amount vs expected_rent.
//
// FIX 4: The original implementation blindly set status='paid' regardless of
// amount. A KES 5,000 CLAIM to a KES 10,000 unit would appear as "paid" in
// LIST. Now:
//   - amount >= expectedRent*2  → overpaid
//   - amount >= expectedRent    → paid
//   - amount < expectedRent     → partial
//
// The update runs inside a transaction so the unit_id and status are written
// atomically. A concurrent payment arriving between the SELECT and UPDATE
// cannot create an inconsistent state because pgx serialises row-level locks.
func (r *Repository) AssignPaymentToUnit(ctx context.Context, paymentID, unitID string, expectedRent, amount int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	// Compute the correct status.
	var newStatus models.PaymentStatus
	switch {
	case amount >= expectedRent*2:
		newStatus = models.PaymentStatusOver
	case amount >= expectedRent:
		newStatus = models.PaymentStatusPaid
	default:
		newStatus = models.PaymentStatusPartial
	}

	tag, err := tx.Exec(ctx, `
		UPDATE payments
		SET    unit_id = $1,
		       status  = $2
		WHERE  id      = $3
	`, unitID, string(newStatus), paymentID)
	if err != nil {
		return fmt.Errorf("assign payment to unit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return models.ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit assign payment: %w", err)
	}
	return nil
}

// InsertManualPayment inserts a landlord-entered cash or bank payment.
func (r *Repository) InsertManualPayment(ctx context.Context, p models.Payment) (*models.Payment, error) {
	var inserted models.Payment
	var scannedUnitID *string
	err := r.db.QueryRow(ctx, `
		INSERT INTO payments (
		    transaction_id, unit_id, landlord_id, tenant_phone,
		    amount, status, month_key, receipt_url, paid_at,
		    payment_source, expected_rent_snapshot
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'',NOW(),$8,$9)
		RETURNING id, transaction_id, unit_id, landlord_id,
		          tenant_phone, amount, status, month_key,
		          receipt_url, paid_at
	`,
		p.TransactionID, p.UnitID, p.LandlordID, p.TenantPhone,
		p.Amount, string(p.Status), p.MonthKey,
		string(p.PaymentSource), p.ExpectedRentSnapshot,
	).Scan(
		&inserted.ID, &inserted.TransactionID, &scannedUnitID, &inserted.LandlordID,
		&inserted.TenantPhone, &inserted.Amount, &inserted.Status,
		&inserted.MonthKey, &inserted.ReceiptURL, &inserted.PaidAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert manual payment: %w", err)
	}
	if scannedUnitID != nil {
		inserted.UnitID = *scannedUnitID
	}
	return &inserted, nil
}

// ── Unit CRUD ─────────────────────────────────────────────────────────────────

// isUniqueViolation returns true for pgx unique-constraint errors (code 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// InsertUnit inserts a new unit and increments the landlord unit_count in one
// transaction.
//
// FIX 6: The previous implementation used ON CONFLICT DO NOTHING which
// silently returned no rows on a duplicate insert. pgx then returned
// pgx.ErrNoRows from QueryRow.Scan, which the caller treated as a generic
// error and — crucially — the unit_count UPDATE still ran inside the same
// transaction, so the counter drifted upward on every duplicate ADD UNIT.
//
// The fix removes ON CONFLICT DO NOTHING and instead lets the unique
// constraint raise a 23505 error, which is translated to models.ErrDuplicate.
// The caller (cmdAddUnit) checks for ErrDuplicate and shows a clear message
// without incrementing unit_count. The counter now only moves when a row is
// actually inserted.
func (r *Repository) InsertUnit(ctx context.Context, u models.Unit) (*models.Unit, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inserted models.Unit
	err = tx.QueryRow(ctx, `
		INSERT INTO units (landlord_id, unit_ref, tenant_name, tenant_phone, expected_rent)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, landlord_id, unit_ref, tenant_name, tenant_phone,
		          expected_rent, active, effective_from, created_at
	`, u.LandlordID, u.UnitRef, u.TenantName, u.TenantPhone, u.ExpectedRent,
	).Scan(
		&inserted.ID, &inserted.LandlordID, &inserted.UnitRef, &inserted.TenantName,
		&inserted.TenantPhone, &inserted.ExpectedRent, &inserted.Active,
		&inserted.EffectiveFrom, &inserted.CreatedAt,
	)
	if err != nil {
		// FIX 6: translate unique constraint to ErrDuplicate so the caller can
		// surface a helpful message without incrementing unit_count.
		if isUniqueViolation(err) {
			return nil, models.ErrDuplicate
		}
		return nil, fmt.Errorf("insert unit: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`UPDATE landlords SET unit_count = unit_count + 1 WHERE id = $1`,
		u.LandlordID,
	); err != nil {
		return nil, fmt.Errorf("update unit count: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &inserted, nil
}

// ReplaceUnitTenant updates tenant details on an existing unit.
func (r *Repository) ReplaceUnitTenant(ctx context.Context, landlordID string, u models.Unit) (*models.Unit, error) {
	var updated models.Unit
	err := r.db.QueryRow(ctx, `
		UPDATE units SET
		    tenant_name    = $3,
		    tenant_phone   = $4,
		    expected_rent  = $5,
		    effective_from = NOW()
		WHERE  landlord_id = $1
		  AND  unit_ref    = $2
		  AND  active      = TRUE
		RETURNING id, landlord_id, unit_ref, tenant_name, tenant_phone,
		          expected_rent, active, effective_from, created_at
	`, landlordID, u.UnitRef, u.TenantName, u.TenantPhone, u.ExpectedRent,
	).Scan(
		&updated.ID, &updated.LandlordID, &updated.UnitRef, &updated.TenantName,
		&updated.TenantPhone, &updated.ExpectedRent, &updated.Active,
		&updated.EffectiveFrom, &updated.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("replace unit tenant: %w", err)
	}
	return &updated, nil
}

// UpdateExpectedRent changes the expected_rent for an active unit.
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

// ── Landlord CRUD (onboarding) ────────────────────────────────────────────────

// CreateLandlord inserts a landlord or updates name/apartment on conflict.
func (r *Repository) CreateLandlord(ctx context.Context, l models.Landlord) (*models.Landlord, error) {
	var created models.Landlord
	err := r.db.QueryRow(ctx, `
		INSERT INTO landlords (whatsapp_phone, name, apartment_name, paybill_number, subscription_status)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (whatsapp_phone) DO UPDATE
		    SET name           = EXCLUDED.name,
		        apartment_name = EXCLUDED.apartment_name
		RETURNING id, whatsapp_phone, name, apartment_name, premise_name,
		          paybill_number, subscription_status, billing_cycle_end,
		          unit_count, created_at
	`, l.WhatsAppPhone, l.Name, l.ApartmentName, l.PaybillNumber,
		string(l.SubscriptionStatus),
	).Scan(
		&created.ID, &created.WhatsAppPhone, &created.Name,
		&created.ApartmentName, &created.PremiseName,
		&created.PaybillNumber, &created.SubscriptionStatus,
		&created.BillingCycleEnd, &created.UnitCount, &created.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create landlord: %w", err)
	}
	return &created, nil
}

// UpdateUnitCount increments the landlord unit_count by delta.
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

// ── Agent / digest helpers ────────────────────────────────────────────────────

// GetLandlordsByAgent returns all landlords managed by an agent.
func (r *Repository) GetLandlordsByAgent(ctx context.Context, agentID string) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, apartment_name, premise_name,
		       paybill_number, subscription_status, billing_cycle_end,
		       unit_count, created_at
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
			&l.ID, &l.WhatsAppPhone, &l.Name, &l.ApartmentName, &l.PremiseName,
			&l.PaybillNumber, &l.SubscriptionStatus, &l.BillingCycleEnd,
			&l.UnitCount, &l.CreatedAt,
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
		SELECT id, whatsapp_phone, name, apartment_name, premise_name,
		       paybill_number, subscription_status, billing_cycle_end,
		       unit_count, created_at
		FROM   landlords
		WHERE  agent_id = $1
		  AND  LOWER(name) LIKE LOWER($2)
		LIMIT  1
	`, agentID, "%"+name+"%").Scan(
		&l.ID, &l.WhatsAppPhone, &l.Name, &l.ApartmentName, &l.PremiseName,
		&l.PaybillNumber, &l.SubscriptionStatus, &l.BillingCycleEnd,
		&l.UnitCount, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("get landlord by name: %w", err)
	}
	return &l, nil
}

// GetLandlordsWithUnpaid returns landlords with at least one unpaid unit this month.
// Used by the 6 PM digest cron.
func (r *Repository) GetLandlordsWithUnpaid(ctx context.Context, monthKey string) ([]models.Landlord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT l.id, l.whatsapp_phone, l.name,
		       l.apartment_name, l.premise_name, l.paybill_number,
		       l.subscription_status, l.billing_cycle_end,
		       l.unit_count, l.created_at
		FROM   landlords l
		JOIN   units u ON u.landlord_id = l.id AND u.active = TRUE
		WHERE  l.subscription_status IN ('active','grace')
		  AND  NOT EXISTS (
		    SELECT 1 FROM payments p
		    WHERE  p.unit_id   = u.id
		      AND  p.month_key = $1
		      AND  p.status   != 'unmatched'
		      AND  p.amount   >= u.expected_rent
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
			&l.ID, &l.WhatsAppPhone, &l.Name,
			&l.ApartmentName, &l.PremiseName, &l.PaybillNumber,
			&l.SubscriptionStatus, &l.BillingCycleEnd,
			&l.UnitCount, &l.CreatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, l)
	}
	return results, rows.Err()
}
