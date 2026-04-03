package models

import "time"

// Receipt represents a generated PDF rent receipt stored in DigitalOcean Spaces.
type Receipt struct {
	ID            string    `db:"id"`
	PaymentID     string    `db:"payment_id"`
	LandlordID    string    `db:"landlord_id"`
	UnitID        string    `db:"unit_id"`
	ReceiptNumber string    `db:"receipt_number"`
	StorageKey    string    `db:"storage_key"`
	PublicURL     string    `db:"public_url"`
	SentToPhone   string    `db:"sent_to_phone"`
	SentAt        time.Time `db:"sent_at"`
}
