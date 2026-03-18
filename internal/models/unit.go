//Package models defines the core domain data structures
//
// These structs represent entities persisted in the database
// and supporting domain types used for rent payment tracking.

package models

import "time"

//Unit represents a single rental unit belonging to a landlord
type Unit struct {
	ID            string    `db:"id"`
	LandlordID    string    `db:"landlord_id"`
	UnitRef       string    `db:"unit_ref"`
	TenantName    string    `db:"tenant_name"`
	TenantPhone   string    `db:"tenant_phone"`
	ExpectedRent  int       `db:"expected_rent"`
	Active        bool      `db:"active"`
	EffectiveFrom time.Time `db:"effective_from"`
	CreatedAt     time.Time `db:"created_at"`
}
