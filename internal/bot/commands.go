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
		if len(parts) >= 2 && parts[1] == "UNIT" {
			return s.cmdAddUnit(ctx, l, strings.Fields(upper)[2:])
		}
		return helpText()
	case "REPLACE":
		return s.cmdReplaceUnit(ctx, l, parts[1:])
	case "SET":
		if len(parts) >= 2 && parts[1] == "RENT" {
			return s.cmdSetRent(ctx, l, parts[2:])
		}
		return helpText()
	case "MARK":
		return s.cmdMark(ctx, l, parts[1:])
	case "CLAIM":
		return s.cmdClaim(ctx, l, parts[1:])
	case "JOIN":
		if s.onboarding != nil {
			s.onboarding.HandleJoin(ctx, l.WhatsAppPhone)
			return ""
		}
		return "You are already registered.\n\nSend *ADD UNIT 4B John Kamau 0712345678 12500* to add a unit."
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
				unpaid = append(unpaid, fmt.Sprintf("✗ Unit %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
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
		err := s.sms.SendReminder(ctx, u.Unit.TenantPhone, u.Unit.TenantName, u.Unit.UnitRef, u.Unit.ExpectedRent, month)
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

func (s *Service) cmdReceipt(ctx context.Context, l *models.Landlord, rawRef string) string {
	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %s not found.", rawRef)
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
		unit.UnitRef, unit.TenantName, h.Amount, time.Now().Format("January 2006"), h.Status,
	)
}

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

func (s *Service) cmdAddUnit(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: ADD UNIT <ref> <name> <phone> <rent>\nExample: ADD UNIT 4B John Kamau 0712345678 12500"
	}

	ref := normaliseRef(parts[0])
	phone := parts[len(parts)-2]
	rent := parseRent(parts[len(parts)-1])
	name := strings.Join(parts[1:len(parts)-2], " ")

	if rent <= 0 {
		return fmt.Sprintf("Invalid rent amount: %s", parts[len(parts)-1])
	}

	inserted, err := s.repo.InsertUnit(ctx, models.Unit{
		LandlordID:   l.ID,
		UnitRef:      ref,
		TenantName:   name,
		TenantPhone:  phone,
		ExpectedRent: rent,
	})
	if err != nil {
		return fmt.Sprintf("Could not add unit %s. It may already exist.", ref)
	}

	return fmt.Sprintf(
		"Unit %s added.\nTenant: %s\nPhone: %s\nRent: KES %d\n\nPaybill: %s · Account: *%s*",
		inserted.UnitRef, inserted.TenantName, inserted.TenantPhone,
		inserted.ExpectedRent, l.PaybillNumber, inserted.UnitRef,
	)
}

// cmdReplaceUnit deactivates the current tenant and inserts a new one.
// Format: REPLACE 4B Grace Auma 0745678901 12500
func (s *Service) cmdReplaceUnit(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: REPLACE <unit> <name> <phone> <rent>\nExample: REPLACE 4B Grace Auma 0745678901 12500"
	}

	ref := normaliseRef(parts[0])
	phone := parts[len(parts)-2]
	rent := parseRent(parts[len(parts)-1])
	name := strings.Join(parts[1:len(parts)-2], " ")

	if rent <= 0 {
		return fmt.Sprintf("Invalid rent amount: %s", parts[len(parts)-1])
	}

	inserted, err := s.repo.ReplaceUnitTenant(ctx, l.ID, models.Unit{
		LandlordID:   l.ID,
		UnitRef:      ref,
		TenantName:   name,
		TenantPhone:  phone,
		ExpectedRent: rent,
	})
	if err != nil {
		return fmt.Sprintf("Could not replace tenant on Unit %s. Please try again.", ref)
	}

	return fmt.Sprintf(
		"Tenant replaced on Unit %s.\nNew tenant: %s\nPhone: %s\nRent: KES %d\n\n"+
			"Previous payment history is preserved.",
		inserted.UnitRef, inserted.TenantName, inserted.TenantPhone, inserted.ExpectedRent,
	)
}

// cmdSetRent updates the expected rent for a unit in the database.
// Format: SET RENT 4B 14000
func (s *Service) cmdSetRent(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 2 {
		return "Usage: SET RENT <unit> <amount>\nExample: SET RENT 4B 14000"
	}

	ref := normaliseRef(parts[0])
	rent := parseRent(parts[1])

	if rent <= 0 {
		return fmt.Sprintf("Invalid rent amount: %s", parts[1])
	}

	if err := s.repo.UpdateExpectedRent(ctx, l.ID, ref, rent); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %s not found.", ref)
		}
		return "Could not update rent. Please try again."
	}

	return fmt.Sprintf(
		"Rent updated.\nUnit %s → KES %d\n\n_Takes effect immediately for future payments._",
		ref, rent,
	)
}

// cmdMark records a manual cash or bank payment in the ledger.
// Format: MARK 4B PAID 12500 BANK
func (s *Service) cmdMark(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: MARK <unit> PAID <amount> <method>\nExample: MARK 4B PAID 12500 BANK"
	}

	ref := normaliseRef(parts[0])
	amount := parseRent(parts[2])
	method := strings.ToUpper(parts[3])

	if amount <= 0 {
		return fmt.Sprintf("Invalid amount: %s", parts[2])
	}

	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found.", ref)
	}

	status := models.PaymentStatusPaid
	if amount >= unit.ExpectedRent*2 {
		status = models.PaymentStatusOver
	} else if amount < unit.ExpectedRent {
		status = models.PaymentStatusPartial
	}

	manualID := fmt.Sprintf("MANUAL-%s-%s-%d", unit.UnitRef, method, time.Now().UnixMilli())

	recorded, err := s.repo.InsertManualPayment(ctx, models.Payment{
		TransactionID: manualID,
		UnitID:        unit.ID,
		LandlordID:    l.ID,
		TenantPhone:   unit.TenantPhone,
		Amount:        amount,
		Status:        status,
		MonthKey:      monthKey(),
	})
	if err != nil {
		return "Could not record payment. Please try again."
	}

	return fmt.Sprintf(
		"*Manual payment recorded*\nUnit %s — %s\nAmount: KES %d\nMethod: %s\nStatus: %s\nRef: #%s",
		unit.UnitRef, unit.TenantName,
		recorded.Amount,
		strings.Title(strings.ToLower(method)),
		string(recorded.Status),
		shortID(recorded.ID),
	)
}

func (s *Service) cmdClaim(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 3 || strings.ToUpper(parts[1]) != "TO" {
		return "Usage: CLAIM <transaction-id> TO <unit>\nExample: CLAIM LHG31AA5TX TO 4B"
	}

	transID := parts[0]
	ref := normaliseRef(parts[2])

	payment, err := s.repo.GetUnmatchedPayment(ctx, l.ID, transID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Transaction %s not found in your unmatched payments.", transID)
		}
		return "Could not find transaction. Please try again."
	}

	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found.", parts[2])
	}

	if err := s.repo.AssignPaymentToUnit(ctx, payment.ID, unit.ID); err != nil {
		return "Could not assign payment. Please try again."
	}

	return fmt.Sprintf(
		"Payment assigned.\nKES %d → Unit %s (%s)\nTransaction: %s",
		payment.Amount, unit.UnitRef, unit.TenantName, transID,
	)
}

// parseRent converts a string like "12500" or "12,500" to int.
func parseRent(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func helpText() string {
	return "*RentLoop Commands*\n\n" +
		"*LIST* — paid vs unpaid this month\n" +
		"*REMIND* — SMS all unpaid tenants\n" +
		"*TOTAL* — collected vs expected\n" +
		"*RECEIPT 4B* — resend receipt for a unit\n" +
		"*HISTORY 4B* — last 3 months for a unit\n" +
		"*ADD UNIT 4B John 0712345678 12500* — add unit\n" +
		"*REPLACE 4B Grace Auma 0745678901 12500* — swap tenant\n" +
		"*SET RENT 4B 14000* — update rent amount\n" +
		"*MARK 4B PAID 12500 BANK* — log cash/bank payment\n" +
		"*CLAIM TXN-ABC123 TO 4B* — assign unmatched payment\n" +
		"*BULK ADD* — upload tenants via CSV"
}
