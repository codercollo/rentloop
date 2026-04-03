package receipt

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/rentloop/internal/models"
)

// Repo is the concrete pgx implementation of Repository.
type Repo struct {
	db *pgxpool.Pool
}

// NewRepository returns a receipt repository backed by pgx.
func NewRepository(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

// InsertReceipt writes a new receipt row.
// ON CONFLICT DO NOTHING so concurrent inserts for the same payment_id are safe.
func (r *Repo) InsertReceipt(ctx context.Context, rec models.Receipt) (*models.Receipt, error) {
	var inserted models.Receipt
	err := r.db.QueryRow(ctx, `
		INSERT INTO receipts
		       (payment_id, landlord_id, unit_id,
		        receipt_number, storage_key, public_url,
		        sent_to_phone, sent_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		ON CONFLICT (receipt_number) DO NOTHING
		RETURNING id, payment_id, landlord_id, unit_id,
		          receipt_number, storage_key, public_url,
		          sent_to_phone, sent_at
	`,
		rec.PaymentID, rec.LandlordID, rec.UnitID,
		rec.ReceiptNumber, rec.StorageKey, rec.PublicURL,
		rec.SentToPhone,
	).Scan(
		&inserted.ID, &inserted.PaymentID, &inserted.LandlordID, &inserted.UnitID,
		&inserted.ReceiptNumber, &inserted.StorageKey, &inserted.PublicURL,
		&inserted.SentToPhone, &inserted.SentAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Conflict — return the existing row.
			return r.GetReceiptByPaymentID(ctx, rec.PaymentID)
		}
		return nil, fmt.Errorf("insert receipt: %w", err)
	}
	return &inserted, nil
}

// GetReceiptByPaymentID returns the receipt for a payment, or nil if none exists.
func (r *Repo) GetReceiptByPaymentID(ctx context.Context, paymentID string) (*models.Receipt, error) {
	var rec models.Receipt
	err := r.db.QueryRow(ctx, `
		SELECT id, payment_id, landlord_id, unit_id,
		       receipt_number, storage_key, public_url,
		       sent_to_phone, sent_at
		FROM   receipts
		WHERE  payment_id = $1
		LIMIT  1
	`, paymentID).Scan(
		&rec.ID, &rec.PaymentID, &rec.LandlordID, &rec.UnitID,
		&rec.ReceiptNumber, &rec.StorageKey, &rec.PublicURL,
		&rec.SentToPhone, &rec.SentAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // caller checks for nil
		}
		return nil, fmt.Errorf("get receipt by payment: %w", err)
	}
	return &rec, nil
}

// GetReceiptsByLandlord returns the last N receipts for a landlord.
func (r *Repo) GetReceiptsByLandlord(ctx context.Context, landlordID string, limit int) ([]models.Receipt, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, payment_id, landlord_id, unit_id,
		       receipt_number, storage_key, public_url,
		       sent_to_phone, sent_at
		FROM   receipts
		WHERE  landlord_id = $1
		ORDER  BY sent_at DESC
		LIMIT  $2
	`, landlordID, limit)
	if err != nil {
		return nil, fmt.Errorf("get receipts by landlord: %w", err)
	}
	defer rows.Close()

	var results []models.Receipt
	for rows.Next() {
		var rec models.Receipt
		if err := rows.Scan(
			&rec.ID, &rec.PaymentID, &rec.LandlordID, &rec.UnitID,
			&rec.ReceiptNumber, &rec.StorageKey, &rec.PublicURL,
			&rec.SentToPhone, &rec.SentAt,
		); err != nil {
			return nil, fmt.Errorf("scan receipt: %w", err)
		}
		results = append(results, rec)
	}
	return results, rows.Err()
}
