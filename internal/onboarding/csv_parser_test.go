// Package onboarding_test contains black-box tests for the CSV parser.
//
// The tests validate CSV ingestion behavior including:
// parsing valid rows, handling headers, skipping blank rows,
// normalizing phone numbers and unit references, and collecting
// validation errors for malformed or incomplete data.
package onboarding_test

import (
	"strings"
	"testing"

	"github.com/codercollo/rentloop/internal/onboarding"
)

func parse(csv string) (*onboarding.ParseResult, error) {
	return onboarding.ParseCSVReader(strings.NewReader(csv))
}

func TestParse_ValidRows_AllLoaded(t *testing.T) {
	input := `unit,name,phone,rent
4B,John Kamau,0712345678,12500
2A,Mary Wanjiku,0723456789,8000
3D,Peter Otieno,0734567890,9500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 3 {
		t.Errorf("expected 3 valid rows, got %d", len(result.Valid))
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(result.Errors))
	}
}

func TestParse_HeaderSkipped(t *testing.T) {
	input := `unit,name,phone,rent
4B,John Kamau,0712345678,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 1 {
		t.Errorf("expected 1 valid row (header skipped), got %d", len(result.Valid))
	}
}

func TestParse_NoHeader_AllLoaded(t *testing.T) {
	input := `4B,John Kamau,0712345678,12500
2A,Mary Wanjiku,0723456789,8000`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 2 {
		t.Errorf("expected 2 rows, got %d", len(result.Valid))
	}
}

func TestParse_BlankUnitRef_GoesToErrors(t *testing.T) {
	input := `,John Kamau,0712345678,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for blank unit ref, got %d", len(result.Errors))
	}
}

func TestParse_BlankTenantName_GoesToErrors(t *testing.T) {
	input := `4B,,0712345678,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for blank name, got %d", len(result.Errors))
	}
}

func TestParse_InvalidPhone_TooShort_GoesToErrors(t *testing.T) {
	input := `4B,John Kamau,07123,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for short phone, got %d", len(result.Errors))
	}
}

func TestParse_InvalidRent_NonNumeric_GoesToErrors(t *testing.T) {
	input := `4B,John Kamau,0712345678,twelve thousand`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for non-numeric rent, got %d", len(result.Errors))
	}
}

func TestParse_ZeroRent_GoesToErrors(t *testing.T) {
	input := `4B,John Kamau,0712345678,0`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error for zero rent, got %d", len(result.Errors))
	}
}

func TestParse_MixedValidAndErrors(t *testing.T) {
	input := `unit,name,phone,rent
4B,John Kamau,0712345678,12500
,Mary Wanjiku,0723456789,8000
3D,Peter Otieno,07345,9500
1A,Grace Auma,0745678901,7000`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 2 {
		t.Errorf("expected 2 valid rows, got %d", len(result.Valid))
	}
	if len(result.Errors) != 2 {
		t.Errorf("expected 2 error rows, got %d", len(result.Errors))
	}
}

func TestParse_PhoneNormalised_To254Format(t *testing.T) {
	input := `4B,John Kamau,0712345678,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 1 {
		t.Fatalf("expected 1 valid row")
	}
	if result.Valid[0].TenantPhone != "254712345678" {
		t.Errorf("expected phone 254712345678, got %s", result.Valid[0].TenantPhone)
	}
}

func TestParse_UnitRefNormalised(t *testing.T) {
	input := `unit 4b,John Kamau,0712345678,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 1 {
		t.Fatalf("expected 1 valid row")
	}
	if result.Valid[0].UnitRef != "4B" {
		t.Errorf("expected normalised ref 4B, got %s", result.Valid[0].UnitRef)
	}
}

func TestParse_RentWithCommas_Accepted(t *testing.T) {
	input := `4B,John Kamau,0712345678,"12,500"`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 1 {
		t.Fatalf("expected 1 valid row, got %d errors: %s", len(result.Errors), result.FormatErrors())
	}
	if result.Valid[0].ExpectedRent != 12500 {
		t.Errorf("expected rent 12500, got %d", result.Valid[0].ExpectedRent)
	}
}

func TestParse_InternationalPhone_Accepted(t *testing.T) {
	input := `4B,John Kamau,+254712345678,12500`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 1 {
		t.Fatalf("expected 1 valid row")
	}
	if result.Valid[0].TenantPhone != "254712345678" {
		t.Errorf("expected 254712345678, got %s", result.Valid[0].TenantPhone)
	}
}

func TestParse_BlankRowsSkipped(t *testing.T) {
	input := `4B,John Kamau,0712345678,12500

2A,Mary Wanjiku,0723456789,8000`

	result, err := parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Valid) != 2 {
		t.Errorf("expected 2 valid rows (blank row skipped), got %d", len(result.Valid))
	}
}

func TestFormatErrors_ReturnsReadableMessage(t *testing.T) {
	input := `,Mary Wanjiku,0723456789,8000`

	result, _ := parse(input)
	msg := result.FormatErrors()

	if !strings.Contains(msg, "Row") {
		t.Errorf("expected error message to contain row reference, got: %s", msg)
	}
}
