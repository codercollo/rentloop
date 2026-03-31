// Package models defines the core domain data structures
//
// These structs represent entities persisted in the database
// and supporting domain types used for rent payment tracking.
package models

import "time"

//PaymentStatus represents the payment state for a rent transaction
type PaymentStatus string

// PaymentSource records the channel through wich payment arrived
type PaymentSource string

const (
	PaymentStatusPaid      PaymentStatus = "paid"
	PaymentStatusPartial   PaymentStatus = "partial"
	PaymentStatusOver      PaymentStatus = "overpaid"
	PaymentStatusUnmatched PaymentStatus = "unmatched"
	PaymentStatusUnknown   PaymentStatus = "unknown"
)

const (
	PaymentSourceMpesaSTK    PaymentSource = "mpesa_stk"
	PaymentSourceBankPaybill PaymentSource = "bank_paybill"
	PaymentSourceManual      PaymentSource = "manual"
)

// DepositTxnType classifies a deposit ledger entry.
type DepositTxnType string

const (
	DepositTxnReceived   DepositTxnType = "received"
	DepositTxnRefund     DepositTxnType = "refund"
	DepositTxnAdjustment DepositTxnType = "adjustment"
)

//Payment represents a processed rent payment mapped
//from an M-Pesa transaction to a specific unit
type Payment struct {
	ID                   string        `db:"id"`
	TransactionID        string        `db:"transaction_id"`
	UnitID               string        `db:"unit_id"`
	LandlordID           string        `db:"landlord_id"`
	TenantPhone          string        `db:"tenant_phone"`
	Amount               int           `db:"amount"`
	Status               PaymentStatus `db:"status"`
	MonthKey             string        `db:"month_key"`
	ExpectedRentSnapshot int           `db:"expected_rent_snapshot"`
	ArrearsCarried       int           `db:"arrears_carried"`
	PaymentSource        PaymentSource `db:"payment_source"`
	ReceiptURL           string        `db:"receipt_url"`
	PaidAt               time.Time     `db:"paid_at"`
}

//MonthBalance represents the aggregated rent payment
type MonthlyBalance struct {
	UnitID       string
	MonthKey     string
	TotalPaid    int
	ExpectedRent int
	Status       PaymentStatus
}

// Deposit domain
//
// Deposit holds the current deposit state for a single unit
type Deposit struct {
	ID              string    `db:"id"`
	UnitID          string    `db:"unit_id"`
	LandlordID      string    `db:"landlord_id"`
	DepositExpected int       `db:"deposit_expected"`
	DepositPaid     int       `db:"deposit_paid"`
	DepositBalance  int       `db:"deposit_balance"` // expected - paid (outstanding)
	DepositRefunded int       `db:"deposit_refunded"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// DepositTransaction is one entry in the deposit audit trail.
type DepositTransaction struct {
	ID         string         `db:"id"`
	DepositID  string         `db:"deposit_id"`
	UnitID     string         `db:"unit_id"`
	LandlordID string         `db:"landlord_id"`
	TxnType    DepositTxnType `db:"txn_type"`
	Amount     int            `db:"amount"`
	Note       string         `db:"note"`
	RecordedAt time.Time      `db:"recorded_at"`
	RecordedBy string         `db:"recorded_by"` // 'landlord' | 'agent' | 'system'
}

// UnitWithStatus combines a unit with its payment state for the current month.
type UnitWithStatus struct {
	Unit      Unit
	IsPaid    bool
	TotalPaid int
}

// MonthlyPaymentRow is one row in a 12-month history query result.
// Used by HISTORY-EXT and LANDLORD-HISTORY commands.
type MonthlyPaymentRow struct {
	MonthKey             string
	AmountPaid           int
	ExpectedRentSnapshot int
	ArrearsCarried       int
	Status               PaymentStatus
	Source               PaymentSource
	ReceiptIDs           []string // receipt/transaction IDs for this month
	PaidAt               *time.Time
}

// PortfolioMonthRow is one month in a landlord portfolio history.
// Used by LANDLORD-HISTORY.
type PortfolioMonthRow struct {
	MonthKey       string
	TotalExpected  int
	TotalCollected int
	TotalArrears   int
	UnitsPaid      int
	UnitsTotal     int
}
