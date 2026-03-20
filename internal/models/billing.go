// Package models defines the core domain data structures
// used by the RentLoop application.
//
// These types represent billing and subscription entities
// used to manage landlord access and payment periods
package models

import "time"

//SubscriptionStatus respresents the billing state of a landlord account
type SubscriptionStatus string

const (
	StatusActive    SubscriptionStatus = "active"
	StatusGrace     SubscriptionStatus = "grace" // within grace period after expiry
	StatusSuspended SubscriptionStatus = "suspended"
)

//SubscriptionPayment represents a payment made by a landlord
type SubscriptionPayment struct {
	ID            string    `db:"id"`
	LandlordID    string    `db:"landlord_id"`
	TransactionID string    `db:"transaction_id"`
	Amount        int       `db:"amount"`
	PaidAt        time.Time `db:"paid_at"`
	PeriodStart   time.Time `db:"period_start"`
	PeriodEnd     time.Time `db:"period_end"`
}
