// Package admin provides the data layer for the RentLoop admin dashboard.
package admin

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository handles all admin dashboard queries.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository returns an admin repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// DashboardStats holds the top-line numbers shown on the dashboard.
type DashboardStats struct {
	TotalLandlords     int
	ActiveLandlords    int
	GraceLandlords     int
	SuspendedLandlords int
	TotalUnits         int
	PaymentsToday      int
	RevenueToday       int
	MRR                int // current month subscription revenue
}

// GetDashboardStats returns aggregate counts for the dashboard.
func (r *Repository) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	var s DashboardStats

	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*)                                                 AS total,
			COUNT(*) FILTER (WHERE subscription_status = 'active')  AS active,
			COUNT(*) FILTER (WHERE subscription_status = 'grace')   AS grace,
			COUNT(*) FILTER (WHERE subscription_status = 'suspended') AS suspended,
			COALESCE(SUM(unit_count), 0)                            AS total_units
		FROM landlords
	`).Scan(&s.TotalLandlords, &s.ActiveLandlords, &s.GraceLandlords, &s.SuspendedLandlords, &s.TotalUnits)
	if err != nil {
		return nil, fmt.Errorf("dashboard stats: %w", err)
	}

	err = r.db.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(amount), 0)
		FROM   payments
		WHERE (paid_at AT TIME ZONE 'Africa/Nairobi')::date
		    = (NOW()   AT TIME ZONE 'Africa/Nairobi')::date
		  AND status != 'unmatched'
	`).Scan(&s.PaymentsToday, &s.RevenueToday)
	if err != nil {
		return nil, fmt.Errorf("payments today: %w", err)
	}

	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM   subscription_payments
		WHERE  TO_CHAR(paid_at, 'YYYY-MM') = TO_CHAR(NOW(), 'YYYY-MM')
	`).Scan(&s.MRR)
	if err != nil {
		return nil, fmt.Errorf("mrr: %w", err)
	}

	return &s, nil
}

// LandlordRow holds one row of the clients table.
type LandlordRow struct {
	models.Landlord
	MonthlyFee int
}

// GetAllLandlords returns all landlords with their monthly fee.
// FIX BUG 2/4: apartment_name added to SELECT and Scan so the clients
// list page can show it without a zero-value blank.
func (r *Repository) GetAllLandlords(ctx context.Context) ([]LandlordRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, whatsapp_phone, name, apartment_name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at,
		       CASE WHEN unit_count > 10 THEN unit_count * 50 ELSE 0 END AS monthly_fee
		FROM   landlords
		ORDER  BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get all landlords: %w", err)
	}
	defer rows.Close()

	var results []LandlordRow
	for rows.Next() {
		var row LandlordRow
		if err := rows.Scan(
			&row.ID, &row.WhatsAppPhone, &row.Name, &row.ApartmentName, &row.PaybillNumber,
			&row.SubscriptionStatus, &row.BillingCycleEnd, &row.UnitCount, &row.CreatedAt,
			&row.MonthlyFee,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// LandlordDetail holds one landlord with their units and recent payments.
type LandlordDetail struct {
	Landlord models.Landlord
	Units    []models.Unit
	Payments []models.Payment
}

// GetLandlordDetail returns full detail for one landlord.
// FIX BUG 3: apartment_name added to SELECT and Scan.
func (r *Repository) GetLandlordDetail(ctx context.Context, id string) (*LandlordDetail, error) {
	var d LandlordDetail

	err := r.db.QueryRow(ctx, `
		SELECT id, whatsapp_phone, name, apartment_name, paybill_number,
		       subscription_status, billing_cycle_end, unit_count, created_at
		FROM   landlords
		WHERE  id = $1
	`, id).Scan(
		&d.Landlord.ID, &d.Landlord.WhatsAppPhone, &d.Landlord.Name,
		&d.Landlord.ApartmentName, &d.Landlord.PaybillNumber,
		&d.Landlord.SubscriptionStatus, &d.Landlord.BillingCycleEnd,
		&d.Landlord.UnitCount, &d.Landlord.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get landlord: %w", err)
	}

	unitRows, err := r.db.Query(ctx, `
		SELECT id, landlord_id, unit_ref, tenant_name, tenant_phone,
		       expected_rent, active, effective_from, created_at
		FROM   units
		WHERE  landlord_id = $1 AND active = TRUE
		ORDER  BY unit_ref
	`, id)
	if err != nil {
		return nil, fmt.Errorf("get units: %w", err)
	}
	defer unitRows.Close()
	for unitRows.Next() {
		var u models.Unit
		if err := unitRows.Scan(
			&u.ID, &u.LandlordID, &u.UnitRef, &u.TenantName, &u.TenantPhone,
			&u.ExpectedRent, &u.Active, &u.EffectiveFrom, &u.CreatedAt,
		); err != nil {
			return nil, err
		}
		d.Units = append(d.Units, u)
	}

	payRows, err := r.db.Query(ctx, `
		SELECT id, transaction_id, COALESCE(unit_id::text, ''), landlord_id,
		       tenant_phone, amount, status, month_key, receipt_url, paid_at
		FROM   payments
		WHERE  landlord_id = $1
		ORDER  BY paid_at DESC
		LIMIT  50
	`, id)
	if err != nil {
		return nil, fmt.Errorf("get payments: %w", err)
	}
	defer payRows.Close()
	for payRows.Next() {
		var p models.Payment
		if err := payRows.Scan(
			&p.ID, &p.TransactionID, &p.UnitID, &p.LandlordID,
			&p.TenantPhone, &p.Amount, &p.Status, &p.MonthKey, &p.ReceiptURL, &p.PaidAt,
		); err != nil {
			return nil, err
		}
		d.Payments = append(d.Payments, p)
	}

	return &d, nil
}

// ActivateLandlord sets a landlord's subscription_status to 'active'.
// Called from the admin panel after receiving Daraja credentials.
func (r *Repository) ActivateLandlord(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE landlords SET subscription_status = 'active' WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("activate landlord: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("activate landlord: id %s not found", id)
	}
	return nil
}

// AgentRow holds one agent with portfolio summary.
type AgentRow struct {
	models.Agent
	LandlordCount int
	MonthlyFee    int
}

// GetAllAgents returns all agents with their portfolio stats.
func (r *Repository) GetAllAgents(ctx context.Context) ([]AgentRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			a.id,
			a.whatsapp_phone,
			a.name,
			a.created_at,
			COUNT(l.id)                                AS landlord_count,
			COALESCE(SUM(l.unit_count), 0)             AS total_units,
			CASE
				WHEN COALESCE(SUM(l.unit_count), 0) > 10
				THEN COALESCE(SUM(l.unit_count), 0) * 50
				ELSE 0
			END                                        AS monthly_fee
		FROM   agents a
		LEFT   JOIN landlords l ON l.agent_id = a.id
		GROUP  BY a.id
		ORDER  BY a.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get all agents: %w", err)
	}
	defer rows.Close()

	var results []AgentRow
	for rows.Next() {
		var row AgentRow
		if err := rows.Scan(
			&row.ID, &row.WhatsAppPhone, &row.Name, &row.CreatedAt,
			&row.LandlordCount, &row.UnitCount, &row.MonthlyFee,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// PaymentRow holds one payment with landlord context for the payments page.
type PaymentRow struct {
	models.Payment
	LandlordName string
	UnitRef      string
}

// GetRecentPayments returns the last 100 payments across all landlords.
func (r *Repository) GetRecentPayments(ctx context.Context) ([]PaymentRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.id, p.transaction_id, COALESCE(p.unit_id::text, ''), p.landlord_id,
		       p.tenant_phone, p.amount, p.status, p.month_key, p.receipt_url, p.paid_at,
		       l.name                      AS landlord_name,
		       COALESCE(u.unit_ref, '—')   AS unit_ref
		FROM   payments p
		JOIN   landlords l ON l.id = p.landlord_id
		LEFT   JOIN units u ON u.id = p.unit_id
		ORDER  BY p.paid_at DESC
		LIMIT  100
	`)
	if err != nil {
		return nil, fmt.Errorf("get recent payments: %w", err)
	}
	defer rows.Close()

	var results []PaymentRow
	for rows.Next() {
		var row PaymentRow
		if err := rows.Scan(
			&row.ID, &row.TransactionID, &row.UnitID, &row.LandlordID,
			&row.TenantPhone, &row.Amount, &row.Status, &row.MonthKey,
			&row.ReceiptURL, &row.PaidAt,
			&row.LandlordName, &row.UnitRef,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// GetUnmatchedPayments returns all unmatched payments with landlord name.
func (r *Repository) GetUnmatchedPayments(ctx context.Context) ([]PaymentRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.id, p.transaction_id, '', p.landlord_id,
		       p.tenant_phone, p.amount, p.status, p.month_key, p.receipt_url, p.paid_at,
		       l.name AS landlord_name, '—' AS unit_ref
		FROM   payments p
		JOIN   landlords l ON l.id = p.landlord_id
		WHERE  p.status = 'unmatched'
		ORDER  BY p.paid_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get unmatched: %w", err)
	}
	defer rows.Close()

	var results []PaymentRow
	for rows.Next() {
		var row PaymentRow
		if err := rows.Scan(
			&row.ID, &row.TransactionID, &row.UnitID, &row.LandlordID,
			&row.TenantPhone, &row.Amount, &row.Status, &row.MonthKey,
			&row.ReceiptURL, &row.PaidAt,
			&row.LandlordName, &row.UnitRef,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func (r *Repository) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&n)
	return n, err
}
