// Package onboarding handles landlord registration and bulk tenant upload.
package onboarding

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/matcher"
	"github.com/codercollo/rentloop/internal/models"
)

// CSVRow holds one validated tenant row from the uploaded CSV.
type CSVRow struct {
	UnitRef      string
	TenantName   string
	TenantPhone  string
	ExpectedRent int
}

// RowError holds a row that failed validation with the reason.
type RowError struct {
	RowNumber int
	Raw       []string
	Reason    string
}

// ParseResult is returned by ParseCSV.
type ParseResult struct {
	Valid  []CSVRow
	Errors []RowError
}

// ParseCSV downloads the CSV from url, parses every row,
// and splits results into valid and error rows.
// The caller decides what to do with partial results.
func ParseCSV(url string) (*ParseResult, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download csv: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download csv: server returned %d", resp.StatusCode)
	}

	return parseReader(resp.Body)
}

// ParseCSVReader parses from an io.Reader directly — used in tests.
func ParseCSVReader(r io.Reader) (*ParseResult, error) {
	return parseReader(r)
}

func parseReader(r io.Reader) (*ParseResult, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1 // allow variable columns, we validate manually

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}

	result := &ParseResult{}

	for i, row := range rows {
		rowNum := i + 1

		// Skip header row — detected by first cell being non-numeric text
		// that looks like a column label
		if i == 0 && looksLikeHeader(row) {
			continue
		}

		// Skip blank rows
		if isBlankRow(row) {
			continue
		}

		validated, reason := validateRow(row)
		if reason != "" {
			result.Errors = append(result.Errors, RowError{
				RowNumber: rowNum,
				Raw:       row,
				Reason:    reason,
			})
			continue
		}

		result.Valid = append(result.Valid, *validated)
	}

	return result, nil
}

// validateRow checks a single CSV row and returns a CSVRow or an error reason.
func validateRow(row []string) (*CSVRow, string) {
	if len(row) < 4 {
		return nil, fmt.Sprintf("expected 4 columns (unit, name, phone, rent), got %d", len(row))
	}

	unitRef := strings.TrimSpace(row[0])
	tenantName := strings.TrimSpace(row[1])
	phone := strings.TrimSpace(row[2])
	rentStr := strings.TrimSpace(row[3])

	if unitRef == "" {
		return nil, "unit ref is blank"
	}

	if tenantName == "" {
		return nil, "tenant name is blank"
	}

	if err := validatePhone(phone); err != nil {
		return nil, err.Error()
	}

	rent, err := strconv.Atoi(strings.ReplaceAll(rentStr, ",", ""))
	if err != nil || rent <= 0 {
		return nil, fmt.Sprintf("rent %q is not a valid positive number", rentStr)
	}

	return &CSVRow{
		UnitRef:      matcher.Normalise(unitRef),
		TenantName:   tenantName,
		TenantPhone:  normalisePhone(phone),
		ExpectedRent: rent,
	}, ""
}

// validatePhone checks that a phone number is plausibly valid.
// Accepts: 07XXXXXXXX, 01XXXXXXXX, +2547XXXXXXXX, 2547XXXXXXXX
func validatePhone(phone string) error {
	digits := digitsOnly(phone)

	switch {
	case len(digits) == 10 && (strings.HasPrefix(digits, "07") || strings.HasPrefix(digits, "01")):
		return nil
	case len(digits) == 12 && strings.HasPrefix(digits, "254"):
		return nil
	case len(digits) == 13 && strings.HasPrefix(digits, "2540"):
		return nil
	default:
		return fmt.Errorf("phone %q is invalid — use 07XXXXXXXX or +2547XXXXXXXX", phone)
	}
}

// normalisePhone converts any Kenyan format to 2547XXXXXXXX.
func normalisePhone(phone string) string {
	digits := digitsOnly(phone)
	switch {
	case len(digits) == 10 && strings.HasPrefix(digits, "0"):
		return "254" + digits[1:]
	case len(digits) == 12 && strings.HasPrefix(digits, "254"):
		return digits
	default:
		return digits
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func looksLikeHeader(row []string) bool {
	if len(row) == 0 {
		return false
	}
	first := strings.ToLower(strings.TrimSpace(row[0]))
	return first == "unit" || first == "unit_ref" || first == "unit ref" || first == "#"
}

func isBlankRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// ToUnits converts valid CSV rows to models.Unit slice for a given landlord.
func (r *ParseResult) ToUnits(landlordID string) []models.Unit {
	units := make([]models.Unit, 0, len(r.Valid))
	for _, row := range r.Valid {
		units = append(units, models.Unit{
			LandlordID:   landlordID,
			UnitRef:      row.UnitRef,
			TenantName:   row.TenantName,
			TenantPhone:  row.TenantPhone,
			ExpectedRent: row.ExpectedRent,
		})
	}
	return units
}

// FormatErrors returns a human-readable error summary for the landlord.
func (r *ParseResult) FormatErrors() string {
	if len(r.Errors) == 0 {
		return ""
	}
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "%d row(s) had errors:\n", len(r.Errors))
	for _, e := range r.Errors {
		raw := strings.Join(e.Raw, ", ")
		fmt.Fprintf(sb, "• Row %d (%s): %s\n", e.RowNumber, raw, e.Reason)
	}
	sb.WriteString("\nFix these rows and resend the corrected CSV.")
	return sb.String()
}
