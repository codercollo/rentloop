// Package matcher resolves raw M-Pesa account references into canonical
// unit identifiers and within the system.
//
// It normalises inconsistent user input (r.g "48", "4 B")
// into a starndard format and performs a lookup against the data store
// via minimal  repository interface.
//
// This ensures that payment references from M-Pesa are reliably matched
// to the correct rental unit regardless of formatting variations.
package matcher

import (
	"context"
	"strings"
	"unicode"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository is the only database dependency matcher needs
type Repository interface {
	GetUnitByRef(ctx context.Context, landlordID, normalisedRef string) (*models.Unit, error)
}

// Matcher resolves a raw M-Pesa account reference to a Unit row
type Matcher struct {
	repo Repository
}

// New returns a ready-to-use Matcher
func New(repo Repository) *Matcher {
	return &Matcher{repo: repo}
}

// Match normalizes rawRef and refurns the matching unit the landlordID
// It trims, uppercase, removes common prefixes (UNIT, APT)
// and strips seperators to inputs like " 4b", "4 B", "unit-4B"
// all resolve to "4B"
//
// Returns models.ErrNoMatch if no unit is found
func (m *Matcher) Match(ctx context.Context, landlordID, rawRef string) (*models.Unit, error) {
	normalised := Normalise(rawRef)
	if normalised == "" {
		return nil, models.ErrNoMatch
	}

	unit, err := m.repo.GetUnitByRef(ctx, landlordID, normalised)
	if err != nil {
		return nil, err
	}

	return unit, nil
}

// Normalise applies all cleaning steps and returns the canonical ref
// Exported so onbording and bot packages use identical rules when
// storing and comparing unit refs
func Normalise(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ToUpper(s)

	for _, prefix := range []string{"APARTMENT", "UNIT", "ROOM", "HOUSE", "APT"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			s = strings.TrimSpace(s)
			break
		}
	}

	// Remove internal whitespace, dashes, underscores
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			return -1
		}
		return r
	}, s)

	return s
}
