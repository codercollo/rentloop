//Package models defines the core domain data structures
//
// These structs represent entities persisted in the database
// and supporting domain types used for rent payment tracking.

package models

import (
	"time"
)

// Landlord represents a property owner
type Landlord struct {
	ID                 string             `db:"id"`
	WhatsAppPhone      string             `db:"whatsapp_phone"`
	Name               string             `db:"name"`
	ApartmentName      string             `db:"apartment_name"`
	PremiseName        string             `db:"premise_name"`
	PaybillNumber      string             `db:"paybill_number"`
	SubscriptionStatus SubscriptionStatus `db:"subscription_status"`
	BillingCycleEnd    *time.Time         `db:"billing_cycle_end"`
	UnitCount          int                `db:"unit_count"`
	CreatedAt          time.Time          `db:"created_at"`
}

// AdminUser represents an administrative account
type AdminUser struct {
	ID              string     `db:"id"`
	Email           string     `db:"email"`
	Password        string     `db:"password"`
	Activated       bool       `db:"activated"`
	ActivationToken string     `db:"activation_token"`
	TokenExpiresAt  *time.Time `db:"token_expires_at"`
	CreatedAt       time.Time  `db:"created_at"`
}
