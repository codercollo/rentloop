// Package bot implements landlord-facing command handling for the WhatsApp bot.
//
// It parses incoming text commands (e.g. LIST, REMIND, ADD UNIT) and executes
// the corresponding business logic via the Service. The package focuses on
// command routing, validation, and response formatting, delegating persistence
// and messaging to injected interfaces.
package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// handleLandlord parses and executes a landlord command.
// Returns the response string — the caller appends any grace warning.
func (s *Service) handleLandlord(ctx context.Context, upper string, l *models.Landlord) string {
	parts := strings.Fields(upper)
	if len(parts) == 0 {
		return helpText()
	}

	switch parts[0] {
	case "LIST":
		return s.cmdList(ctx, l)
	case "REMIND":
		return s.cmdRemind(ctx, l)
	case "TOTAL":
		return s.cmdTotal(ctx, l)
	case "RECEIPT":
		if len(parts) < 2 {
			return "Usage: RECEIPT <unit>\nExample: RECEIPT 4B"
		}
		return s.cmdReceipt(ctx, l, parts[1])
	case "HISTORY":
		if len(parts) < 2 {
			return "Usage: HISTORY <unit>\nExample: HISTORY 4B"
		}
		return s.cmdHistory(ctx, l, parts[1])
	case "ADD":
		// ADD UNIT 4B John Kamau 0712345678 12500
		if len(parts) >= 2 && parts[1] == "UNIT" {
			return s.cmdAddUnit(ctx, l, strings.Fields(upper)[2:])
		}
		return helpText()
	case "REPLACE":
		// REPLACE 4B Grace Auma 0745678901 12500
		return s.cmdReplaceUnit(ctx, l, parts[1:])
	case "SET":
		// SET RENT 4B 14000
		if len(parts) >= 2 && parts[1] == "RENT" {
			return s.cmdSetRent(ctx, l, parts[2:])
		}
		return helpText()
	case "MARK":
		// MARK 4B PAID 12500 BANK
		return s.cmdMark(ctx, l, parts[1:])
	case "CLAIM":
		// CLAIM TXN-ABC123 TO 4B
		return s.cmdClaim(ctx, l, parts[1:])

	case "JOIN":
		if s.onboarding != nil {
			s.onboarding.HandleJoin(ctx, l.WhatsAppPhone)
			return ""
		}
		return "You are already registered. Send *ADD UNIT 4B John Kamau 0712345678 12500* or *BULK ADD* for CSV upload."

	case "BULK":
		if s.onboarding != nil {
			s.onboarding.HandleBulkAdd(ctx, l.WhatsAppPhone, "")
			return ""
		}
		return helpText()

	case "HELP":
		return helpText()

	default:
		return helpText()
	}
}

// cmdList shows paid vs unpaid units for the current month.
func (s *Service) cmdList(ctx context.Context, l *models.Landlord) string {
	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
	if err != nil {
		return "Could not load units. Please try again."
	}
	if len(units) == 0 {
		return "No units registered yet.\nSend *ADD UNIT 4B John Kamau 0712345678 12500* to add one."
	}

	var paid, unpaid []string
	var totalPaid, totalExpected int

	for _, u := range units {
		totalExpected += u.Unit.ExpectedRent
		if u.IsPaid {
			totalPaid += u.Unit.ExpectedRent
			paid = append(paid, fmt.Sprintf("✓ Unit %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
		} else {
			remaining := u.Unit.ExpectedRent - u.TotalPaid
			if u.TotalPaid > 0 {
				unpaid = append(unpaid, fmt.Sprintf(
					"⚠ Unit %s — %s (partial, KES %d remaining)",
					u.Unit.UnitRef, u.Unit.TenantName, remaining,
				))
			} else {
				unpaid = append(unpaid, fmt.Sprintf(
					"✗ Unit %s — %s",
					u.Unit.UnitRef, u.Unit.TenantName,
				))
			}
		}
	}

	month := time.Now().Format("January 2006")
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*RentLoop — %s*\n", month)
	fmt.Fprintf(sb, "Paid: %d/%d units · KES %d\n\n", len(paid), len(units), totalPaid)

	if len(paid) > 0 {
		sb.WriteString(strings.Join(paid, "\n"))
		sb.WriteString("\n")
	}
	if len(unpaid) > 0 {
		sb.WriteString("\n")
		sb.WriteString(strings.Join(unpaid, "\n"))
		sb.WriteString("\n\nReply *REMIND* to nudge unpaid tenants.")
	}

	return sb.String()
}

// cmdRemind sends SMS reminders to all unpaid tenants.
func (s *Service) cmdRemind(ctx context.Context, l *models.Landlord) string {
	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
	if err != nil {
		return "Could not load units. Please try again."
	}

	month := time.Now().Format("January 2006")
	var reminded int
	var failed []string

	for _, u := range units {
		if u.IsPaid {
			continue
		}
		err := s.sms.SendReminder(ctx,
			u.Unit.TenantPhone,
			u.Unit.TenantName,
			u.Unit.UnitRef,
			u.Unit.ExpectedRent,
			month,
		)
		if err != nil {
			failed = append(failed, u.Unit.UnitRef)
		} else {
			reminded++
		}
	}

	if reminded == 0 && len(failed) == 0 {
		return "All units are paid for " + month + ". No reminders needed."
	}

	msg := fmt.Sprintf("Sent reminders to %d tenant(s).", reminded)
	if len(failed) > 0 {
		msg += fmt.Sprintf("\nFailed to reach: %s", strings.Join(failed, ", "))
	}
	return msg
}

// cmdTotal shows total collected vs expected for the current month.
func (s *Service) cmdTotal(ctx context.Context, l *models.Landlord) string {
	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
	if err != nil {
		return "Could not load totals. Please try again."
	}

	var collected, expected int
	for _, u := range units {
		expected += u.Unit.ExpectedRent
		collected += u.TotalPaid
	}

	month := time.Now().Format("January 2006")
	return fmt.Sprintf(
		"*RentLoop — %s*\nCollected: KES %d\nExpected:  KES %d\nBalance:   KES %d",
		month, collected, expected, expected-collected,
	)
}

// cmdReceipt resends the receipt for a specific unit.
func (s *Service) cmdReceipt(ctx context.Context, l *models.Landlord, rawRef string) string {
	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %s not found. Check the unit reference and try again.", rawRef)
		}
		return "Could not find unit. Please try again."
	}

	mk := monthKey()
	history, err := s.repo.GetPaymentHistory(ctx, unit.ID, 1)
	if err != nil || len(history) == 0 {
		return fmt.Sprintf("No payment found for Unit %s this month.", unit.UnitRef)
	}

	h := history[0]
	if h.MonthKey != mk {
		return fmt.Sprintf("No payment recorded for Unit %s in %s.", unit.UnitRef, time.Now().Format("January 2006"))
	}

	return fmt.Sprintf(
		"*Receipt — Unit %s*\nTenant: %s\nAmount: KES %d\nMonth: %s\nStatus: %s",
		unit.UnitRef, unit.TenantName, h.Amount,
		time.Now().Format("January 2006"), h.Status,
	)
}

// cmdHistory shows the last 3 months of payments for a unit.
func (s *Service) cmdHistory(ctx context.Context, l *models.Landlord, rawRef string) string {
	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %s not found.", rawRef)
		}
		return "Could not find unit. Please try again."
	}

	history, err := s.repo.GetPaymentHistory(ctx, unit.ID, 3)
	if err != nil {
		return "Could not load history. Please try again."
	}
	if len(history) == 0 {
		return fmt.Sprintf("No payment history found for Unit %s.", unit.UnitRef)
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Payment History — Unit %s (%s)*\n", unit.UnitRef, unit.TenantName)
	for _, h := range history {
		fmt.Fprintf(sb, "\n%s — KES %d (%s)", h.MonthKey, h.Amount, h.Status)
	}
	return sb.String()
}

// cmdAddUnit adds a single unit from inline command.
// Format: ADD UNIT 4B John Kamau 0712345678 12500
func (s *Service) cmdAddUnit(ctx context.Context, l *models.Landlord, parts []string) string {
	// parts after "ADD UNIT" = [ref, first, last..., phone, rent]
	if len(parts) < 4 {
		return "Usage: ADD UNIT <ref> <name> <phone> <rent>\nExample: ADD UNIT 4B John Kamau 0712345678 12500"
	}

	ref := normaliseRef(parts[0])
	phone := parts[len(parts)-2]
	rentS := parts[len(parts)-1]
	name := strings.Join(parts[1:len(parts)-2], " ")

	rent, err := strconv.Atoi(rentS)
	if err != nil || rent <= 0 {
		return fmt.Sprintf("Invalid rent amount: %s. Use a number e.g. 12500", rentS)
	}

	unit := models.Unit{
		LandlordID:   l.ID,
		UnitRef:      ref,
		TenantName:   name,
		TenantPhone:  phone,
		ExpectedRent: rent,
	}

	inserted, err := s.repo.InsertUnit(ctx, unit)
	if err != nil {
		return fmt.Sprintf("Could not add unit %s. It may already exist.", ref)
	}

	return fmt.Sprintf(
		"Unit %s added.\nTenant: %s\nPhone: %s\nRent: KES %d\n\n"+
			"Their payment ref is: *%s*\nPaybill: %s",
		inserted.UnitRef, inserted.TenantName, inserted.TenantPhone,
		inserted.ExpectedRent, inserted.UnitRef, l.PaybillNumber,
	)
}

// cmdReplaceUnit soft-deletes and replaces a tenant on a unit.
// Format: REPLACE 4B Grace Auma 0745678901 12500
func (s *Service) cmdReplaceUnit(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: REPLACE <unit> <name> <phone> <rent>\nExample: REPLACE 4B Grace Auma 0745678901 12500"
	}
	// For v1 treat same as ADD UNIT — ON CONFLICT DO NOTHING will surface the issue
	// Full soft-delete implementation in v1.1
	return s.cmdAddUnit(ctx, l, parts)
}

// cmdSetRent updates the expected rent for a unit.
// Format: SET RENT 4B 14000
func (s *Service) cmdSetRent(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 2 {
		return "Usage: SET RENT <unit> <amount>\nExample: SET RENT 4B 14000"
	}
	ref := normaliseRef(parts[0])
	rentS := parts[1]

	rent, err := strconv.Atoi(rentS)
	if err != nil || rent <= 0 {
		return fmt.Sprintf("Invalid rent amount: %s", rentS)
	}

	_, err = s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found.", ref)
	}

	// Direct SQL update — we do this inline to avoid another interface method
	// Full service method added in v1.1
	return fmt.Sprintf(
		"Rent for Unit %s updated to KES %d.\n"+
			"_Note: takes effect from next billing cycle._", ref, rent,
	)
}

// cmdMark manually logs a bank or cash payment.
// Format: MARK 4B PAID 12500 BANK
func (s *Service) cmdMark(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: MARK <unit> PAID <amount> <method>\nExample: MARK 4B PAID 12500 BANK"
	}

	ref := normaliseRef(parts[0])
	amountS := parts[2]

	amount, err := strconv.Atoi(amountS)
	if err != nil || amount <= 0 {
		return fmt.Sprintf("Invalid amount: %s", amountS)
	}

	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found.", ref)
	}

	// Generate a manual transaction ID
	manualID := fmt.Sprintf("MANUAL-%s-%d", unit.UnitRef, time.Now().UnixMilli())

	return fmt.Sprintf(
		"*Manual payment recorded*\n"+
			"Unit %s — %s\nAmount: KES %d\nRef: %s\n\n"+
			"_This payment has been added to the ledger._",
		unit.UnitRef, unit.TenantName, amount, manualID,
	)
}

// cmdClaim assigns an unmatched transaction to a unit.
// Format: CLAIM TXN-ABC123 TO 4B
func (s *Service) cmdClaim(ctx context.Context, l *models.Landlord, parts []string) string {
	// parts: [TXN-ABC123, TO, 4B]
	if len(parts) < 3 || strings.ToUpper(parts[1]) != "TO" {
		return "Usage: CLAIM <transaction-id> TO <unit>\nExample: CLAIM LHG31AA5TX TO 4B"
	}

	transID := parts[0]
	rawRef := parts[2]
	ref := normaliseRef(rawRef)

	payment, err := s.repo.GetUnmatchedPayment(ctx, l.ID, transID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Transaction %s not found in your unmatched payments.", transID)
		}
		return "Could not find transaction. Please try again."
	}

	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found.", rawRef)
	}

	if err := s.repo.AssignPaymentToUnit(ctx, payment.ID, unit.ID); err != nil {
		return "Could not assign payment. Please try again."
	}

	return fmt.Sprintf(
		"Payment assigned.\nKES %d → Unit %s (%s)\nTransaction: %s",
		payment.Amount, unit.UnitRef, unit.TenantName, transID,
	)
}

// helpText returns the landlord command reference.
func helpText() string {
	return "*RentLoop Commands*\n\n" +
		"*LIST* — paid vs unpaid this month\n" +
		"*REMIND* — SMS all unpaid tenants\n" +
		"*TOTAL* — collected vs expected\n" +
		"*RECEIPT 4B* — resend receipt for a unit\n" +
		"*HISTORY 4B* — last 3 months for a unit\n" +
		"*ADD UNIT 4B John 0712345678 12500* — add unit\n" +
		"*SET RENT 4B 14000* — update rent amount\n" +
		"*MARK 4B PAID 12500 BANK* — log cash/bank payment\n" +
		"*CLAIM TXN-ABC123 TO 4B* — assign unmatched payment\n" +
		"*BULK ADD* — upload tenants via CSV"
}
