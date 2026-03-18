// Package models defines the core domain data structures
// used by the RentLoop application.
package models

import "time"

// Agent represents a property manager who manages multiple
// landlords on their behalf. A nil AgentID on a Landlord
// means that landlord is self-managed (no agent).
type Agent struct {
	ID            string    `db:"id"`
	WhatsAppPhone string    `db:"whatsapp_phone"`
	Name          string    `db:"name"`
	UnitCount     int       `db:"unit_count"` // sum across all managed landlords
	CreatedAt     time.Time `db:"created_at"`
}
