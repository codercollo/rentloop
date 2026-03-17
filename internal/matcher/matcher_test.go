// Package matcher_test contains black-box tests for the matcher package.
//
// The tests verify that raw M-Pesa account references are correctly
// normalised and resolved to the appropriate unit using only the
// exported API.
//
// A mock repository is used to isolate behaviour and ensure correctness
// of matching logic, including input normalisation, error handling,
// and landlord-level data isolation.
package matcher_test

import (
	"context"
	"testing"

	"github.com/codercollo/rentloop/internal/matcher"
	"github.com/codercollo/rentloop/internal/models"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	units map[string]*models.Unit // key: "landlordID:normalisedRef"
}

func (m *mockRepo) GetUnitByRef(_ context.Context, landlordID, ref string) (*models.Unit, error) {
	key := landlordID + ":" + ref
	unit, ok := m.units[key]
	if !ok {
		return nil, models.ErrNoMatch
	}
	return unit, nil
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		units: map[string]*models.Unit{
			"ll-1:4B":  {ID: "u-1", UnitRef: "4B", TenantName: "John Kamau"},
			"ll-1:12A": {ID: "u-2", UnitRef: "12A", TenantName: "Mary Wanjiku"},
			"ll-2:4B":  {ID: "u-3", UnitRef: "4B", TenantName: "Peter Otieno"},
		},
	}
}

// ── Normalise tests ───────────────────────────────────────────────────────────

func TestNormalise(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic cases
		{"4B", "4B"},
		{"4b", "4B"},
		{"4 B", "4B"},
		{" 4B ", "4B"},

		// Prefix stripping
		{"unit4B", "4B"},
		{"UNIT4B", "4B"},
		{"UNIT 4B", "4B"},
		{"Unit 4B", "4B"},
		{"apt4B", "4B"},
		{"APT 4B", "4B"},
		{"apartment 4B", "4B"},
		{"APARTMENT4B", "4B"},
		{"room 4B", "4B"},
		{"HOUSE 4B", "4B"},

		// Separator removal
		{"4-B", "4B"},
		{"4_B", "4B"},
		{"4 - B", "4B"},

		// Multi-char refs
		{"12A", "12A"},
		{"12a", "12A"},
		{"unit 12A", "12A"},

		// Edge cases
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		got := matcher.Normalise(tt.input)
		if got != tt.expected {
			t.Errorf("Normalise(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// ── Match tests ───────────────────────────────────────────────────────────────

func TestMatch_ExactRef_ReturnsUnit(t *testing.T) {
	m := matcher.New(newMockRepo())
	unit, err := m.Match(context.Background(), "ll-1", "4B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unit.TenantName != "John Kamau" {
		t.Errorf("got tenant %q want %q", unit.TenantName, "John Kamau")
	}
}

func TestMatch_LowercaseRef_ReturnsUnit(t *testing.T) {
	m := matcher.New(newMockRepo())
	unit, err := m.Match(context.Background(), "ll-1", "4b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unit.ID != "u-1" {
		t.Errorf("got unit ID %q want %q", unit.ID, "u-1")
	}
}

func TestMatch_SpacedRef_ReturnsUnit(t *testing.T) {
	m := matcher.New(newMockRepo())
	unit, err := m.Match(context.Background(), "ll-1", "4 B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unit.ID != "u-1" {
		t.Errorf("got unit ID %q want %q", unit.ID, "u-1")
	}
}

func TestMatch_UnitPrefix_ReturnsUnit(t *testing.T) {
	m := matcher.New(newMockRepo())

	cases := []string{"unit4B", "UNIT4B", "UNIT 4B", "Unit 4B"}
	for _, raw := range cases {
		unit, err := m.Match(context.Background(), "ll-1", raw)
		if err != nil {
			t.Errorf("Match(%q): unexpected error: %v", raw, err)
			continue
		}
		if unit.ID != "u-1" {
			t.Errorf("Match(%q): got unit %q want u-1", raw, unit.ID)
		}
	}
}

func TestMatch_DashRef_ReturnsUnit(t *testing.T) {
	m := matcher.New(newMockRepo())
	unit, err := m.Match(context.Background(), "ll-1", "4-B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unit.ID != "u-1" {
		t.Errorf("got unit ID %q want u-1", unit.ID)
	}
}

func TestMatch_UnknownRef_ReturnsErrNoMatch(t *testing.T) {
	m := matcher.New(newMockRepo())
	_, err := m.Match(context.Background(), "ll-1", "ZZ99")
	if err != models.ErrNoMatch {
		t.Errorf("expected ErrNoMatch got %v", err)
	}
}

func TestMatch_EmptyRef_ReturnsErrNoMatch(t *testing.T) {
	m := matcher.New(newMockRepo())
	_, err := m.Match(context.Background(), "ll-1", "")
	if err != models.ErrNoMatch {
		t.Errorf("expected ErrNoMatch got %v", err)
	}
}

func TestMatch_WhitespaceOnlyRef_ReturnsErrNoMatch(t *testing.T) {
	m := matcher.New(newMockRepo())
	_, err := m.Match(context.Background(), "ll-1", "   ")
	if err != models.ErrNoMatch {
		t.Errorf("expected ErrNoMatch got %v", err)
	}
}

// Same ref under different landlords must resolve to the correct unit.
func TestMatch_SameRefDifferentLandlords_ReturnsScopedUnit(t *testing.T) {
	m := matcher.New(newMockRepo())

	unit1, err := m.Match(context.Background(), "ll-1", "4B")
	if err != nil {
		t.Fatalf("ll-1 match error: %v", err)
	}

	unit2, err := m.Match(context.Background(), "ll-2", "4B")
	if err != nil {
		t.Fatalf("ll-2 match error: %v", err)
	}

	if unit1.ID == unit2.ID {
		t.Error("same ref under different landlords returned same unit — scoping is broken")
	}
	if unit1.TenantName != "John Kamau" {
		t.Errorf("ll-1 unit: got %q want John Kamau", unit1.TenantName)
	}
	if unit2.TenantName != "Peter Otieno" {
		t.Errorf("ll-2 unit: got %q want Peter Otieno", unit2.TenantName)
	}
}

// A ref belonging to ll-1 must not be visible from ll-2.
func TestMatch_CrossLandlordIsolation(t *testing.T) {
	m := matcher.New(newMockRepo())

	// 12A only exists under ll-1
	_, err := m.Match(context.Background(), "ll-2", "12A")
	if err != models.ErrNoMatch {
		t.Errorf("expected ErrNoMatch for cross-landlord access, got %v", err)
	}
}
