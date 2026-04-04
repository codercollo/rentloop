// Package bot implements command parsing and execution logic for landlord
// WhatsApp interactions. It processes inbound text commands, routes them to
// the appropriate handlers, and interacts with repositories, notification
// services, and payment systems to manage units, tenants, and rent payments.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// apartmentLabel returns premise_name when set, falling back to apartment_name,
// then "RentLoop". Always prefers the fully-qualified premise name so that
// "Sunrise Apartments — Kitengela" appears instead of just "Sunrise Apartments".
func apartmentLabel(l *models.Landlord) string {
	if l.PremiseName != "" {
		return l.PremiseName
	}
	if l.ApartmentName != "" {
		return l.ApartmentName
	}
	return "RentLoop"
}

// handleLandlord parses and executes a landlord command.
func (s *Service) handleLandlord(ctx context.Context, upper string, l *models.Landlord) string {
	// ── Subscription gate ─────────────────────────────────────────────────────
	if l.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(l.ID)
	}

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
			return "Usage: RECEIPT <unit>\nExample: RECEIPT A1"
		}
		return s.cmdReceipt(ctx, l, parts[1])
	case "HISTORY":
		if len(parts) < 2 {
			return "Usage: HISTORY <unit>\nExample: HISTORY A1"
		}
		return s.cmdHistory(ctx, l, parts[1])
	case "ANNUAL":
		if len(parts) < 3 || strings.ToUpper(parts[1]) != "HISTORY" {
			return "Usage: ANNUAL HISTORY <unit>\nExample: ANNUAL HISTORY A1"
		}
		return s.cmdHistoryExt(ctx, l, parts[2])
	case "LANDLORD-HISTORY":
		return s.cmdLandlordHistory(ctx, l)
	case "DEPOSIT":
		if len(parts) < 2 {
			return "Usage: DEPOSIT <unit>\nExample: DEPOSIT A1"
		}
		return s.cmdDeposit(ctx, l, parts[1])
	case "DEPOSIT-RECEIVED":
		if len(parts) < 3 {
			return "Usage: DEPOSIT-RECEIVED <unit> <amount>\nExample: DEPOSIT-RECEIVED A1 15000"
		}
		note := ""
		if len(parts) > 3 {
			note = strings.Join(parts[3:], " ")
		}
		return s.cmdDepositReceive(ctx, l, parts[1], parts[2], note)
	case "DEPOSIT-REFUND":
		if len(parts) < 3 {
			return "Usage: DEPOSIT-REFUND <unit> <amount>\nExample: DEPOSIT-REFUND A1 15000"
		}
		note := ""
		if len(parts) > 3 {
			note = strings.Join(parts[3:], " ")
		}
		return s.cmdDepositRefund(ctx, l, parts[1], parts[2], note)
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
		return "You are already registered.\n\nSend *ADD UNIT A1 John Kamau 0712345678 12500* to add a unit."
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

// ── LIST / TOTAL / REMIND ─────────────────────────────────────────────────────

func (s *Service) cmdList(ctx context.Context, l *models.Landlord) string {
	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
	if err != nil {
		return "Could not load units. Please try again."
	}
	if len(units) == 0 {
		return "No units registered yet.\nSend *ADD UNIT A1 John Kamau 0712345678 12500* to add one."
	}

	var paid, unpaid []string
	var paidCount, totalPaid, totalExpected int

	for _, u := range units {
		totalExpected += u.Unit.ExpectedRent
		if u.IsPaid {
			paidCount++
			totalPaid += u.TotalPaid
			paid = append(paid, fmt.Sprintf("✓ Unit %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
		} else {
			remaining := u.Unit.ExpectedRent - u.TotalPaid
			if u.TotalPaid > 0 {
				unpaid = append(unpaid, fmt.Sprintf(
					"⚠ Unit %s — %s (partial, KES %s remaining)",
					u.Unit.UnitRef, u.Unit.TenantName, formatAmount(remaining),
				))
			} else {
				unpaid = append(unpaid, fmt.Sprintf("✗ Unit %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
			}
		}
	}

	month := time.Now().Format("January 2006")
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*%s — %s*\n", apartmentLabel(l), month)
	fmt.Fprintf(sb, "Paid: %d/%d units · KES %s\n\n", paidCount, len(units), formatAmount(totalPaid))
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
		// Remind both fully unpaid AND partial payers.
		// IsPaid is only true when the unit is fully settled.
		if u.IsPaid {
			continue
		}

		// For partial payers, pass what's still owed — not the full expected rent.
		// For zero payers, remaining == ExpectedRent, so the message is still correct.
		remaining := u.Unit.ExpectedRent - u.TotalPaid

		err := s.sms.SendReminder(
			ctx,
			u.Unit.TenantPhone,
			u.Unit.TenantName,
			u.Unit.UnitRef,
			remaining,
			u.TotalPaid,
			month,
		)
		if err != nil {
			slog.Error("remind: send failed",
				"unit", u.Unit.UnitRef,
				"phone", u.Unit.TenantPhone,
				"error", err,
			)
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

// cmdTotal shows rent collected vs expected with a hard visual separator
// before the deposit section so the two figures can never be confused.
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
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*%s — %s*\n", apartmentLabel(l), month)

	// ── Rent section ──────────────────────────────────────────────────────────
	fmt.Fprintf(sb, "\n*Rent collected*\n")
	fmt.Fprintf(sb, "Collected: KES %s\n", formatAmount(collected))
	fmt.Fprintf(sb, "Expected:  KES %s\n", formatAmount(expected))
	switch {
	case collected > expected:
		overpaid := collected - expected
		fmt.Fprintf(sb, "Balance:   fully collected ✓\n")
		fmt.Fprintf(sb, "Overpaid:  KES %s above expected", formatAmount(overpaid))
	case collected == expected:
		fmt.Fprintf(sb, "Balance:   fully collected ✓")
	default:
		fmt.Fprintf(sb, "Balance:   KES %s outstanding", formatAmount(expected-collected))
	}

	// ── Deposit section — hard separator so rent and deposit never mix ────────
	if s.deposits != nil {
		var totalHeld, totalOutstanding int
		for _, u := range units {
			ref := normaliseRef(u.Unit.UnitRef)
			dep, err := s.deposits.Get(ctx, l.ID, ref)
			if err != nil || (dep.Deposit.DepositPaid == 0 && dep.Deposit.DepositExpected == 0) {
				continue
			}
			d := dep.Deposit
			if d.DepositBalance > 0 {
				totalOutstanding += d.DepositBalance
			}
			totalHeld += d.DepositPaid - d.DepositRefunded
		}
		if totalHeld > 0 || totalOutstanding > 0 {
			// Hard rule — makes it impossible to confuse deposit with rent totals.
			fmt.Fprintf(sb, "\n\n───────────────\n")
			fmt.Fprintf(sb, "*Security deposits*\n")
			fmt.Fprintf(sb, "Held:        KES %s", formatAmount(totalHeld))
			if totalOutstanding > 0 {
				fmt.Fprintf(sb, "\nOutstanding: KES %s from tenants", formatAmount(totalOutstanding))
			}
			fmt.Fprintf(sb, "\n_(deposits are not rent — do not add these figures together)_")
		}
	}

	return sb.String()
}

// ── RECEIPT / HISTORY ─────────────────────────────────────────────────────────

// cmdReceipt shows the aggregated monthly receipt for a unit.
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
	rows, err := s.repo.GetPaymentHistory12(ctx, unit.ID)
	if err != nil || len(rows) == 0 {
		return fmt.Sprintf("No payment found for Unit %s this month.", unit.UnitRef)
	}

	// GetPaymentHistory12 returns newest-first; rows[0] is the most recent month.
	r := rows[0]
	if r.MonthKey != mk {
		return fmt.Sprintf("No payment recorded for Unit %s in %s.",
			unit.UnitRef, time.Now().Format("January 2006"))
	}

	return fmt.Sprintf(
		"*Receipt — %s*\n*Unit %s*\nTenant: %s\nAmount: KES %s\nMonth: %s\nStatus: %s",
		apartmentLabel(l),
		unit.UnitRef, unit.TenantName,
		formatAmount(r.AmountPaid),
		time.Now().Format("January 2006"),
		string(r.Status),
	)
}

// cmdHistory shows the last 3 months for a unit with prominent arrears display.
// Arrears are surfaced at the top as a running total so the landlord immediately
// sees whether money is owed before reading the per-month breakdown.
func (s *Service) cmdHistory(ctx context.Context, l *models.Landlord, rawRef string) string {
	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %s not found.", rawRef)
		}
		return "Could not find unit. Please try again."
	}

	// Use GetPaymentHistory12 then take last 3 rows, so arrears show.
	rows, err := s.repo.GetPaymentHistory12(ctx, unit.ID)
	if err != nil {
		return "Could not load history. Please try again."
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No payment history found for Unit %s.", unit.UnitRef)
	}

	// Take up to 3 most recent months (rows are newest-first).
	limit := 3
	if len(rows) < limit {
		limit = len(rows)
	}
	rows = rows[:limit]

	// ── Compute a running arrears total across the visible window ─────────────
	// We sum ArrearsCarried from the oldest visible row — this represents the
	// cumulative unpaid balance a landlord should act on.
	var totalArrears int
	var totalShortfall int
	for _, r := range rows {
		if r.Status == models.PaymentStatusPartial {
			owes := r.ExpectedRentSnapshot - r.AmountPaid
			if owes > 0 {
				totalShortfall += owes
			}
		}
		// ArrearsCarried on the most-recent row is the most meaningful figure;
		// take the largest value seen (oldest row carries the full cumulative debt).
		if r.ArrearsCarried > totalArrears {
			totalArrears = r.ArrearsCarried
		}
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Payment History — Unit %s (%s)*\n", unit.UnitRef, unit.TenantName)
	fmt.Fprintf(sb, "%s\n", apartmentLabel(l))

	// ── Arrears banner — shown only when there is an outstanding balance ──────
	// Displayed before the per-month rows so it cannot be missed.
	outstandingTotal := totalArrears
	if totalShortfall > outstandingTotal {
		outstandingTotal = totalShortfall
	}
	if outstandingTotal > 0 {
		fmt.Fprintf(sb, "\n⚠ *Arrears: KES %s outstanding*\n", formatAmount(outstandingTotal))
	}

	// ── Per-month rows ────────────────────────────────────────────────────────
	for _, r := range rows {
		icon := statusEmoji(r.Status)
		fmt.Fprintf(sb, "\n%s %s — KES %s / KES %s",
			icon,
			formatMonthKey(r.MonthKey),
			formatAmount(r.AmountPaid),
			formatAmount(r.ExpectedRentSnapshot),
		)

		if r.Status == models.PaymentStatusPartial {
			owes := r.ExpectedRentSnapshot - r.AmountPaid
			if owes > 0 {
				fmt.Fprintf(sb, "\n  ↳ KES %s still owed", formatAmount(owes))
			}
		}

		if r.ArrearsCarried > 0 {
			fmt.Fprintf(sb, "\n  ↳ KES %s carried forward from prior months", formatAmount(r.ArrearsCarried))
		}
	}

	// ── Prompt to act if arrears are present ─────────────────────────────────
	if outstandingTotal > 0 {
		fmt.Fprintf(sb, "\n\nReply *ANNUAL HISTORY %s* for the full 12-month view.", unit.UnitRef)
	}

	return sb.String()
}

// ── HISTORY-EXT ───────────────────────────────────────────────────────────────

func (s *Service) cmdHistoryExt(ctx context.Context, l *models.Landlord, rawRef string) string {
	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, l.ID, ref)
	if err != nil {
		if isNotFound(err) {
			return fmt.Sprintf("Unit %s not found.", rawRef)
		}
		return "Could not find unit. Please try again."
	}

	rows, err := s.repo.GetPaymentHistory12(ctx, unit.ID)
	if err != nil {
		return "Could not load extended history. Please try again."
	}

	startMonth := unit.CreatedAt.Format("2006-01")
	var filtered []models.MonthlyPaymentRow
	for _, r := range rows {
		if r.MonthKey >= startMonth {
			filtered = append(filtered, r)
		}
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*12-Month History — Unit %s (%s)*\n", unit.UnitRef, unit.TenantName)
	fmt.Fprintf(sb, "%s\n", apartmentLabel(l))

	if len(filtered) == 0 {
		fmt.Fprintf(sb, "\n✗ %s\n  Paid: KES 0 / KES %s\n  No payment recorded yet.",
			formatMonthKey(monthKey()),
			formatAmount(unit.ExpectedRent),
		)
	} else {
		// ── Arrears banner across all 12 months ───────────────────────────────
		var maxArrears, totalShortfall int
		for _, r := range filtered {
			if r.ArrearsCarried > maxArrears {
				maxArrears = r.ArrearsCarried
			}
			if r.Status == models.PaymentStatusPartial {
				owes := r.ExpectedRentSnapshot - r.AmountPaid
				if owes > 0 {
					totalShortfall += owes
				}
			}
		}
		outstandingTotal := maxArrears
		if totalShortfall > outstandingTotal {
			outstandingTotal = totalShortfall
		}
		if outstandingTotal > 0 {
			fmt.Fprintf(sb, "\n⚠ *Total arrears: KES %s*\n", formatAmount(outstandingTotal))
		}

		for _, r := range filtered {
			icon := statusEmoji(r.Status)
			fmt.Fprintf(sb, "\n%s %s\n  Paid: KES %s / KES %s",
				icon,
				formatMonthKey(r.MonthKey),
				formatAmount(r.AmountPaid),
				formatAmount(r.ExpectedRentSnapshot),
			)
			if r.Status == models.PaymentStatusPartial {
				owes := r.ExpectedRentSnapshot - r.AmountPaid
				if owes > 0 {
					fmt.Fprintf(sb, "\n  ↳ KES %s still owed", formatAmount(owes))
				}
			}
			if r.ArrearsCarried > 0 {
				fmt.Fprintf(sb, "\n  ↳ KES %s carried forward", formatAmount(r.ArrearsCarried))
			}
			if r.Source == models.PaymentSourceBankPaybill {
				fmt.Fprintf(sb, "\n  Via: bank paybill")
			}
			if len(r.ReceiptIDs) > 0 {
				fmt.Fprintf(sb, "\n  Receipt: #%s", shortID(r.ReceiptIDs[0]))
			}
		}
	}

	// ── Deposit block — hard separator so rent and deposit never mix ──────────
	if s.deposits != nil {
		dep, depErr := s.deposits.Get(ctx, l.ID, ref)
		if depErr == nil && dep.Deposit.DepositExpected > 0 {
			fmt.Fprintf(sb, "\n\n───────────────\n")
			fmt.Fprintf(sb, "*Security deposit*\n")
			fmt.Fprintf(sb, "Expected: KES %s\n", formatAmount(dep.Deposit.DepositExpected))
			fmt.Fprintf(sb, "Paid:     KES %s\n", formatAmount(dep.Deposit.DepositPaid))
			if dep.Deposit.DepositRefunded > 0 {
				fmt.Fprintf(sb, "Refunded: KES %s\n", formatAmount(dep.Deposit.DepositRefunded))
			}
			held := dep.Deposit.DepositPaid - dep.Deposit.DepositRefunded
			switch {
			case dep.Deposit.DepositBalance > 0:
				fmt.Fprintf(sb, "Status:   KES %s outstanding", formatAmount(dep.Deposit.DepositBalance))
			case held > 0:
				fmt.Fprintf(sb, "Status:   KES %s held", formatAmount(held))
			default:
				fmt.Fprintf(sb, "Status:   fully refunded ✓")
			}
		}
	}

	return sb.String()
}

// ── LANDLORD-HISTORY ──────────────────────────────────────────────────────────

func (s *Service) cmdLandlordHistory(ctx context.Context, l *models.Landlord) string {
	rows, err := s.repo.GetPortfolioHistory12(ctx, l.ID)
	if err != nil {
		return "Could not load portfolio history. Please try again."
	}
	if len(rows) == 0 {
		return "No payment history recorded yet."
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*12-Month Portfolio — %s*\n", apartmentLabel(l))

	for _, r := range rows {
		icon := portfolioIcon(r.TotalCollected, r.TotalExpected)
		fmt.Fprintf(sb, "\n%s %s — %d/%d units\n",
			icon, formatMonthKey(r.MonthKey), r.UnitsPaid, r.UnitsTotal)
		fmt.Fprintf(sb, "  Collected: KES %s\n", formatAmount(r.TotalCollected))
		fmt.Fprintf(sb, "  Expected:  KES %s", formatAmount(r.TotalExpected))
		shortfall := r.TotalExpected - r.TotalCollected
		if shortfall > 0 {
			fmt.Fprintf(sb, "\n  Shortfall: KES %s", formatAmount(shortfall))
		}
		if r.TotalArrears > 0 && r.TotalArrears != shortfall {
			fmt.Fprintf(sb, "\n  ↳ Arrears: KES %s carried forward", formatAmount(r.TotalArrears))
		}
	}

	return sb.String()
}

// ── DEPOSIT commands ──────────────────────────────────────────────────────────

// cmdDeposit shows the deposit state for a unit in a clear, Kenyan-style format.
func (s *Service) cmdDeposit(ctx context.Context, l *models.Landlord, rawRef string) string {
	if s.deposits == nil {
		return "Deposit tracking is not available. Please contact support."
	}

	ref := normaliseRef(rawRef)
	result, err := s.deposits.Get(ctx, l.ID, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %s not found.", rawRef)
		}
		return "Could not load deposit info. Please try again."
	}

	dep := result.Deposit
	unit := result.Unit

	if dep.DepositExpected == 0 && dep.DepositPaid == 0 {
		return fmt.Sprintf(
			"*Deposit — Unit %s*\nTenant: %s\n\n"+
				"No deposit on record.\n\n"+
				"*Record one:*\n"+
				"DEPOSIT-RECEIVE %s <amount>",
			unit.UnitRef, unit.TenantName, unit.UnitRef,
		)
	}

	held := dep.DepositPaid - dep.DepositRefunded

	var statusLine string
	switch {
	case dep.DepositBalance > 0:
		statusLine = fmt.Sprintf("⚠ KES %s still outstanding from tenant", formatAmount(dep.DepositBalance))
	case held > 0:
		statusLine = fmt.Sprintf("✓ KES %s held", formatAmount(held))
	default:
		statusLine = "✓ Fully refunded"
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Security deposit — Unit %s*\n", unit.UnitRef)
	fmt.Fprintf(sb, "Tenant: %s\n\n", unit.TenantName)
	fmt.Fprintf(sb, "Expected deposit: KES %s\n", formatAmount(dep.DepositExpected))
	fmt.Fprintf(sb, "Total received:   KES %s\n", formatAmount(dep.DepositPaid))
	if dep.DepositRefunded > 0 {
		fmt.Fprintf(sb, "Total refunded:   KES %s\n", formatAmount(dep.DepositRefunded))
	}
	fmt.Fprintf(sb, "Held:             KES %s\n", formatAmount(held))
	fmt.Fprintf(sb, "Status:           %s\n", statusLine)

	if len(result.Transactions) > 0 {
		fmt.Fprintf(sb, "\n*Recent activity:*\n")
		limit := 3
		if len(result.Transactions) < limit {
			limit = len(result.Transactions)
		}
		for _, t := range result.Transactions[:limit] {
			label := "Received"
			if t.TxnType == models.DepositTxnRefund {
				label = "Refund"
			}
			fmt.Fprintf(sb, "%s — %s KES %s\n",
				t.RecordedAt.In(eatLocation()).Format("02 Jan"),
				label,
				formatAmount(t.Amount),
			)
		}
	}

	fmt.Fprintf(sb, "\n*Reply:*\n")
	fmt.Fprintf(sb, "• DEPOSIT-RECEIVED %s <amount>\n", unit.UnitRef)
	fmt.Fprintf(sb, "• DEPOSIT-REFUND %s <amount>", unit.UnitRef)

	return sb.String()
}

func (s *Service) cmdDepositReceive(ctx context.Context, l *models.Landlord, rawRef, amountStr, note string) string {
	if s.deposits == nil {
		return "Deposit tracking is not available. Please contact support."
	}

	ref := normaliseRef(rawRef)
	amount := parseRent(amountStr)
	if amount <= 0 {
		return fmt.Sprintf("Invalid amount: %s\nUsage: DEPOSIT-RECEIVED <unit> <amount>", amountStr)
	}

	result, err := s.deposits.Receive(ctx, l.ID, ref, amount, note, "landlord")
	if err != nil {
		return fmt.Sprintf("Could not record deposit: %v", err)
	}

	dep := result.Deposit
	unit := result.Unit

	held := dep.DepositPaid - dep.DepositRefunded
	remaining := dep.DepositExpected - dep.DepositPaid

	var statusLine string
	switch {
	case remaining > 0:
		statusLine = fmt.Sprintf("⚠ KES %s still outstanding from tenant", formatAmount(remaining))
	default:
		statusLine = fmt.Sprintf("✓ KES %s held", formatAmount(held))
	}

	return fmt.Sprintf(
		"*Security deposit received ✓*\n"+
			"Unit %s — %s\n\n"+
			"Received:         KES %s\n"+
			"Total received:   KES %s\n"+
			"Expected deposit: KES %s\n"+
			"Status:           %s",
		unit.UnitRef, unit.TenantName,
		formatAmount(amount),
		formatAmount(dep.DepositPaid),
		formatAmount(dep.DepositExpected),
		statusLine,
	)
}

func (s *Service) cmdDepositRefund(ctx context.Context, l *models.Landlord, rawRef, amountStr, note string) string {
	if s.deposits == nil {
		return "Deposit tracking is not available. Please contact support."
	}

	ref := normaliseRef(rawRef)
	amount := parseRent(amountStr)
	if amount <= 0 {
		return fmt.Sprintf("Invalid amount: %s\nUsage: DEPOSIT-REFUND <unit> <amount>", amountStr)
	}

	result, err := s.deposits.Refund(ctx, l.ID, ref, amount, note, "landlord")
	if err != nil {
		return fmt.Sprintf("Refund failed: %v", err)
	}

	dep := result.Deposit
	unit := result.Unit
	held := dep.DepositPaid - dep.DepositRefunded

	refID := ""
	if len(result.Transactions) > 0 {
		refID = shortID(result.Transactions[0].ID)
	}

	var statusLine string
	if held > 0 {
		statusLine = fmt.Sprintf("KES %s still held", formatAmount(held))
	} else {
		statusLine = "Fully refunded ✓"
	}

	dateStr := time.Now().In(eatLocation()).Format("02 Jan 2006")

	return fmt.Sprintf(
		"*Security deposit refund recorded ✓*\n"+
			"Unit %s — %s\n\n"+
			"Refunded:       KES %s\n"+
			"Total refunded: KES %s\n"+
			"Remaining held: KES %s\n"+
			"Status:         %s\n"+
			"Date:           %s\n"+
			"Ref:            #%s",
		unit.UnitRef, unit.TenantName,
		formatAmount(amount),
		formatAmount(dep.DepositRefunded),
		formatAmount(held),
		statusLine,
		dateStr,
		refID,
	)
}

// ── Unit management commands ──────────────────────────────────────────────────

func (s *Service) cmdAddUnit(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: ADD UNIT <ref> <name> <phone> <rent>\nExample: ADD UNIT A1 John Kamau 0712345678 12500"
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
		if errors.Is(err, models.ErrDuplicate) {
			return fmt.Sprintf(
				"Unit %s already exists.\n\nTo update the tenant, use:\n*REPLACE %s <name> <phone> <rent>*",
				ref, ref,
			)
		}
		return fmt.Sprintf("Could not add unit %s. Please try again.", ref)
	}

	return fmt.Sprintf(
		"Unit %s added to *%s*.\nTenant: %s\nPhone: %s\nRent: KES %s\n\nPaybill: %s · Account: *%s*",
		inserted.UnitRef, apartmentLabel(l),
		inserted.TenantName, inserted.TenantPhone,
		formatAmount(inserted.ExpectedRent),
		l.PaybillNumber, inserted.UnitRef,
	)
}

func (s *Service) cmdReplaceUnit(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: REPLACE <unit> <name> <phone> <rent>\nExample: REPLACE A1 Grace Auma 0745678901 12500"
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
		"Tenant replaced on Unit %s.\nNew tenant: %s\nPhone: %s\nRent: KES %s\n\n"+
			"Previous payment history is preserved.",
		inserted.UnitRef, inserted.TenantName, inserted.TenantPhone,
		formatAmount(inserted.ExpectedRent),
	)
}

func (s *Service) cmdSetRent(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 2 {
		return "Usage: SET RENT <unit> <amount>\nExample: SET RENT A1 14000"
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
		"Rent updated.\nUnit %s → KES %s\n\n_Takes effect immediately for future payments._",
		ref, formatAmount(rent),
	)
}

func (s *Service) cmdMark(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 4 {
		return "Usage: MARK <unit> PAID <amount> <method>\nExample: MARK A1 PAID 12500 BANK"
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
		TransactionID:        manualID,
		UnitID:               unit.ID,
		LandlordID:           l.ID,
		TenantPhone:          unit.TenantPhone,
		Amount:               amount,
		Status:               status,
		MonthKey:             monthKey(),
		PaymentSource:        models.PaymentSourceManual,
		ExpectedRentSnapshot: unit.ExpectedRent,
	})
	if err != nil {
		return "Could not record payment. Please try again."
	}

	//nolint:staticcheck
	response := fmt.Sprintf(
		"*Manual payment recorded*\nUnit %s — %s\nAmount: KES %s\nMethod: %s\nStatus: %s\nRef: #%s",
		unit.UnitRef, unit.TenantName,
		formatAmount(recorded.Amount),
		strings.Title(strings.ToLower(method)),
		string(recorded.Status),
		shortID(recorded.ID),
	)

	switch status {
	case models.PaymentStatusPartial:
		remaining := unit.ExpectedRent - amount
		response += fmt.Sprintf(
			"\n\n_Note: expected KES %s — KES %s still outstanding._",
			formatAmount(unit.ExpectedRent),
			formatAmount(remaining),
		)
	case models.PaymentStatusOver:
		excess := amount - unit.ExpectedRent
		response += fmt.Sprintf(
			"\n\n_Note: expected KES %s — KES %s overpaid._",
			formatAmount(unit.ExpectedRent),
			formatAmount(excess),
		)
	}

	return response
}

func (s *Service) cmdClaim(ctx context.Context, l *models.Landlord, parts []string) string {
	if len(parts) < 3 || strings.ToUpper(parts[1]) != "TO" {
		return "Usage: CLAIM <transaction-id> TO <unit>\nExample: CLAIM LHG31AA5TX TO A1"
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

	if err := s.repo.AssignPaymentToUnit(ctx, payment.ID, unit.ID, unit.ExpectedRent, payment.Amount); err != nil {
		return "Could not assign payment. Please try again."
	}

	return fmt.Sprintf(
		"Payment of KES %s assigned to Unit %s (%s)\nTransaction: %s",
		formatAmount(payment.Amount), unit.UnitRef, unit.TenantName, transID,
	)
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func formatAmount(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		pos := len(s) - i
		if i > 0 && pos%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func formatMonthKey(mk string) string {
	if len(mk) != 7 {
		return mk
	}
	t, err := time.Parse("2006-01", mk)
	if err != nil {
		return mk
	}
	return t.Format("Jan 2006")
}

func statusEmoji(s models.PaymentStatus) string {
	switch s {
	case models.PaymentStatusPaid:
		return "✓"
	case models.PaymentStatusPartial:
		return "⚠"
	case models.PaymentStatusOver:
		return "⬆"
	default:
		return "✗"
	}
}

func portfolioIcon(collected, expected int) string {
	if expected == 0 {
		return "·"
	}
	pct := collected * 100 / expected
	switch {
	case pct >= 100:
		return "✓"
	case pct >= 80:
		return "⚠"
	default:
		return "✗"
	}
}

func parseRent(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func suspendedMsg(landlordID string) string {
	ref := landlordID
	if len(ref) > 8 {
		ref = ref[:8]
	}
	return "*Suspended*\n\n" +
		"Your RentLoop subscription has lapsed.\n" +
		"Reply *PAY* to renew via M-Pesa STK Push.\n\n" +
		"Or pay manually: Paybill *400200* · Account *RENTLOOP-" + ref + "*"
}

func eatLocation() *time.Location {
	loc, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		return time.UTC
	}
	return loc
}

func helpText() string {
	return "*RentLoop Commands*\n\n" +
		"*LIST* — paid vs unpaid this month\n" +
		"*REMIND* — reminders to all unpaid tenants\n" +
		"*TOTAL* — rent collected vs expected (deposits shown separately)\n" +
		"*RECEIPT <unit>* — receipt for a unit this month\n" +
		"*HISTORY <unit>* — last 3 months with arrears summary\n" +
		"*ANNUAL HISTORY <unit>* — full 12-month history with arrears\n" +
		"*LANDLORD-HISTORY* — 12-month portfolio overview\n" +
		"*DEPOSIT <unit>* — deposit balance for a unit\n" +
		"*DEPOSIT-RECEIVED <unit> 15000* — record deposit received\n" +
		"*DEPOSIT-REFUND <unit> 12000* — record deposit refund\n" +
		"*ADD UNIT <unit> John 0712345678 12500* — add a unit\n" +
		"*REPLACE <unit> Grace Auma 0745678901 12500* — swap tenant\n" +
		"*SET RENT <unit> 14000* — update rent amount\n" +
		"*MARK <unit> PAID 12500 BANK* — log cash/bank payment\n" +
		"*CLAIM TXN-ABC123 TO <unit>* — assign unmatched payment\n" +
		"*BULK ADD* — upload tenants via CSV\n" +
		"*PAY* — renew subscription via M-Pesa STK Push"
}
