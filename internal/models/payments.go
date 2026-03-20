// Package models defines the core domain data structures
//
// These structs represent entities persisted in the database
// and supporting domain types used for rent payment tracking.
package models

import "time"

//PaymentStatus represents the payment state for a rent transaction
type PaymentStatus string

const (
	PaymentStatusPaid    PaymentStatus = "paid"
	PaymentStatusPartial PaymentStatus = "partial"
	PaymentStatusOver    PaymentStatus = "overpaid"
)

//Payment represents a processed rent payment mapped
//from an M-Pesa transaction to a specific unit
type Payment struct {
	ID            string        `db:"id"`
	TransactionID string        `db:"transaction_id"`
	UnitID        string        `db:"unit_id"`
	LandlordID    string        `db:"landlord_id"`
	TenantPhone   string        `db:"tenant_phone"`
	Amount        int           `db:"amount"`
	Status        PaymentStatus `db:"status"`
	MonthKey      string        `db:"month_key"`
	ReceiptURL    string        `db:"receipt_url"`
	PaidAt        time.Time     `db:"paid_at"`
}

//MonthBalance represents the aggregated rent payment
type MonthlyBalance struct {
	UnitID       string
	MonthKey     string
	TotalPaid    int
	ExpectedRent int
	Status       PaymentStatus
}
