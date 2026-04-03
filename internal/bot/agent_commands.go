// Package bot implements agent-facing command handling for the WhatsApp bot.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// handleAgent parses and executes an agent command.
func (s *Service) handleAgent(ctx context.Context, from, upper string, a *models.Agent) {
	parts := strings.Fields(upper)
	if len(parts) == 0 {
		s.reply(ctx, from, agentHelpText())
		return
	}

	var response string

	switch parts[0] {
	case "CLIENTS":
		response = s.agentCmdClients(ctx, a)

	case "LIST":
		if len(parts) < 2 {
			response = s.agentCmdListAll(ctx, a)
		} else {
			response = s.agentCmdList(ctx, a, strings.Join(parts[1:], " "))
		}

	case "REMIND":
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "ALL" {
			response = s.agentCmdRemindAll(ctx, a)
		} else if len(parts) < 2 {
			response = "Usage: REMIND <landlord name> or REMIND ALL"
		} else {
			response = s.agentCmdRemind(ctx, a, strings.Join(parts[1:], " "))
		}

	case "TOTAL":
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "ALL" {
			response = s.agentCmdTotalAll(ctx, a)
		} else if len(parts) >= 2 {
			response = s.agentCmdTotal(ctx, a, strings.Join(parts[1:], " "))
		} else {
			response = s.agentCmdTotalAll(ctx, a)
		}

	case "RECEIPT":
		if len(parts) < 3 {
			response = "Usage: RECEIPT <landlord name> <unit>\nExample: RECEIPT Wanjiku A1"
		} else {
			unitRef := parts[len(parts)-1]
			name := strings.Join(parts[1:len(parts)-1], " ")
			response = s.agentCmdReceipt(ctx, a, name, unitRef)
		}

	case "HISTORY":
		if len(parts) < 3 {
			response = "Usage: HISTORY <landlord name> <unit>\nExample: HISTORY Wanjiku A1"
		} else {
			unitRef := parts[len(parts)-1]
			name := strings.Join(parts[1:len(parts)-1], " ")
			response = s.agentCmdHistory(ctx, a, name, unitRef)
		}

	case "HISTORY-EXT":
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "ALL" {
			response = s.agentCmdHistoryExtAll(ctx, a)
		} else if len(parts) < 3 {
			response = "Usage: HISTORY-EXT <landlord name> <unit> or HISTORY-EXT ALL\nExample: HISTORY-EXT Wanjiku A1"
		} else {
			unitRef := parts[len(parts)-1]
			name := strings.Join(parts[1:len(parts)-1], " ")
			response = s.agentCmdHistoryExt(ctx, a, name, unitRef)
		}

	case "LANDLORD-HISTORY":
		if len(parts) < 2 {
			response = "Usage: LANDLORD-HISTORY <landlord name>\nExample: LANDLORD-HISTORY Wanjiku"
		} else {
			response = s.agentCmdLandlordHistory(ctx, a, strings.Join(parts[1:], " "))
		}

	case "DEPOSIT":
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "STATUS" {
			if len(parts) >= 3 && strings.ToUpper(parts[2]) == "ALL" {
				response = s.agentCmdDepositAll(ctx, a)
			} else if len(parts) >= 3 {
				response = s.agentCmdDepositByLandlord(ctx, a, strings.Join(parts[2:], " "))
			} else {
				response = s.agentCmdDepositAll(ctx, a)
			}
		} else if len(parts) < 3 {
			response = "Usage: DEPOSIT <landlord name> <unit>\n" +
				"       DEPOSIT STATUS ALL\n" +
				"       DEPOSIT STATUS <landlord name>"
		} else {
			unitRef := parts[len(parts)-1]
			name := strings.Join(parts[1:len(parts)-1], " ")
			response = s.agentCmdDeposit(ctx, a, name, unitRef)
		}

	case "PAY":
		// PAY <landlord name>
		if len(parts) < 2 {
			response = "Usage: PAY <landlord name>\nExample: PAY Wanjiku"
		} else {
			response = s.agentCmdPay(ctx, from, a, strings.Join(parts[1:], " "))
		}

	case "BILLING":
		// BILLING <landlord name> or BILLING ALL
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "ALL" {
			response = s.agentCmdBillingAll(ctx, a)
		} else if len(parts) < 2 {
			response = s.agentCmdBillingAll(ctx, a)
		} else {
			response = s.agentCmdBilling(ctx, a, strings.Join(parts[1:], " "))
		}

	case "HELP":
		response = agentHelpText()
	default:
		response = agentHelpText()
	}

	s.reply(ctx, from, response)
}

// ── Shared formatting helpers ─────────────────────────────────────────────────

func unitPayLine(u UnitStatus) string {
	switch {
	case u.IsPaid:
		return fmt.Sprintf("  ✓ %s — %s — Paid", u.Unit.UnitRef, u.Unit.TenantName)
	case u.TotalPaid > 0:
		remaining := u.Unit.ExpectedRent - u.TotalPaid
		return fmt.Sprintf("  ⚠ %s — %s — Partial, KES %s left",
			u.Unit.UnitRef, u.Unit.TenantName, formatAmount(remaining))
	default:
		return fmt.Sprintf("  ✗ %s — %s — Not paid", u.Unit.UnitRef, u.Unit.TenantName)
	}
}

func depositStatusSummary(d *models.Deposit) string {
	held := d.DepositPaid - d.DepositRefunded
	switch {
	case d.DepositBalance > 0:
		return fmt.Sprintf("KES %s outstanding", formatAmount(d.DepositBalance))
	case held > 0:
		return fmt.Sprintf("KES %s held", formatAmount(held))
	default:
		return "fully refunded"
	}
}

// ── CLIENTS ───────────────────────────────────────────────────────────────────

func (s *Service) agentCmdClients(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*RentLoop — Your Clients (%d)*\n", len(landlords))
	for i := range landlords {
		l := &landlords[i]
		icon := "✓"
		if l.SubscriptionStatus == models.StatusGrace {
			icon = "⚠"
		} else if l.SubscriptionStatus == models.StatusSuspended {
			icon = "✗"
		}
		fmt.Fprintf(sb, "\n%s %s — %d units", icon, apartmentLabel(l), l.UnitCount)
	}
	sb.WriteString("\n\nReply *LIST <name>* to check a client's payment status.")
	return sb.String()
}

// ── LIST ──────────────────────────────────────────────────────────────────────

func (s *Service) agentCmdListAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	mk := monthKey()
	month := time.Now().Format("January 2006")
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Portfolio — %s*\n", month)

	var grandPaid, grandTotal int

	for i := range landlords {
		l := &landlords[i]
		if l.SubscriptionStatus == models.StatusSuspended {
			fmt.Fprintf(sb, "\n✗ *%s* — suspended\n", apartmentLabel(l))
			continue
		}

		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil || len(units) == 0 {
			continue
		}

		var apPaid int
		for _, u := range units {
			if u.IsPaid {
				apPaid++
			}
		}
		grandPaid += apPaid
		grandTotal += len(units)

		label := apartmentLabel(l)
		fmt.Fprintf(sb, "\n*%s* (%d/%d paid)\n", label, apPaid, len(units))
		for _, u := range units {
			fmt.Fprintln(sb, unitPayLine(u))
		}
	}

	if grandTotal == 0 {
		return "No active units found across your portfolio."
	}
	fmt.Fprintf(sb, "\n*Total: %d/%d units paid*", grandPaid, grandTotal)
	return sb.String()
}

func (s *Service) agentCmdList(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q. Check the name and try again.", name)
		}
		return "Could not find landlord. Please try again."
	}

	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, landlord.ID, mk)
	if err != nil {
		return "Could not load units. Please try again."
	}
	if len(units) == 0 {
		return fmt.Sprintf("%s has no units yet.", landlord.Name)
	}

	var paidCount int
	var totalPaid, totalExpected int
	month := time.Now().Format("January 2006")
	label := apartmentLabel(landlord)

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*%s — %s*\n", label, month)

	for _, u := range units {
		totalExpected += u.Unit.ExpectedRent
		totalPaid += u.TotalPaid
		if u.IsPaid {
			paidCount++
		}
		fmt.Fprintln(sb, unitPayLine(u))
	}

	fmt.Fprintf(sb, "\nPaid: %d/%d · KES %s collected", paidCount, len(units), formatAmount(totalPaid))
	if paidCount < len(units) {
		fmt.Fprintf(sb, "\n\nReply *REMIND %s* to nudge unpaid tenants.", landlord.Name)
	}
	return sb.String()
}

// ── REMIND ────────────────────────────────────────────────────────────────────

func (s *Service) agentCmdRemindAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}

	mk := monthKey()
	month := time.Now().Format("January 2006")

	var reminded []string
	var failed []string

	for i := range landlords {
		l := &landlords[i]
		if l.SubscriptionStatus == models.StatusSuspended {
			continue
		}
		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil {
			continue
		}
		label := apartmentLabel(l)
		for _, u := range units {
			if u.IsPaid {
				continue
			}
			remaining := u.Unit.ExpectedRent - u.TotalPaid
			err := s.sms.SendReminder(ctx,
				u.Unit.TenantPhone, u.Unit.TenantName,
				u.Unit.UnitRef, remaining, u.TotalPaid, month,
			)
			entry := fmt.Sprintf("  %s — %s (%s)", u.Unit.TenantName, u.Unit.UnitRef, label)
			if err != nil {
				failed = append(failed, entry)
			} else {
				reminded = append(reminded, entry)
			}
		}
	}

	if len(reminded) == 0 && len(failed) == 0 {
		return fmt.Sprintf("All units paid for %s — no reminders needed. ✓", month)
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*REMIND ALL — %s*\n", month)
	if len(reminded) > 0 {
		fmt.Fprintf(sb, "Sent: %d · Failed: %d\n", len(reminded), len(failed))
	}
	if len(reminded) > 0 {
		fmt.Fprintf(sb, "\n✅ Reminded:\n%s", strings.Join(reminded, "\n"))
	}
	if len(failed) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(sb, "✗ Could not reach:\n%s", strings.Join(failed, "\n"))
	}
	return sb.String()
}

func (s *Service) agentCmdRemind(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord. Please try again."
	}

	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, landlord.ID, mk)
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
		remaining := u.Unit.ExpectedRent - u.TotalPaid
		if err := s.sms.SendReminder(ctx,
			u.Unit.TenantPhone, u.Unit.TenantName,
			u.Unit.UnitRef, remaining, u.TotalPaid, month,
		); err == nil {
			reminded++
		} else {
			failed = append(failed, u.Unit.UnitRef)
		}
	}

	if reminded == 0 && len(failed) == 0 {
		return fmt.Sprintf("All of %s's units are paid for %s. ✓", landlord.Name, month)
	}
	msg := fmt.Sprintf("Sent %d reminder(s) for %s.", reminded, landlord.Name)
	if len(failed) > 0 {
		msg += fmt.Sprintf("\nFailed to reach: %s", strings.Join(failed, ", "))
	}
	return msg
}

// ── TOTAL ─────────────────────────────────────────────────────────────────────

// agentCmdTotalAll shows per-landlord rent totals with a hard separator before
// any deposit figures so the two can never be confused.
func (s *Service) agentCmdTotalAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	mk := monthKey()
	month := time.Now().Format("January 2006")

	var grandCollected, grandExpected int
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Portfolio — Rent — %s*\n", month)

	for i := range landlords {
		l := &landlords[i]
		if l.SubscriptionStatus == models.StatusSuspended {
			fmt.Fprintf(sb, "\n✗ *%s* — suspended\n", apartmentLabel(l))
			continue
		}
		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil || len(units) == 0 {
			continue
		}

		var collected, expected, overdue int
		for _, u := range units {
			expected += u.Unit.ExpectedRent
			collected += u.TotalPaid
			if !u.IsPaid {
				overdue++
			}
		}
		grandCollected += collected
		grandExpected += expected

		label := apartmentLabel(l)
		icon := portfolioIcon(collected, expected)
		fmt.Fprintf(sb, "\n%s *%s*\n", icon, label)
		fmt.Fprintf(sb, "  Collected: KES %s / KES %s",
			formatAmount(collected), formatAmount(expected))
		if overdue > 0 {
			fmt.Fprintf(sb, "\n  ⚠ %d unit(s) overdue", overdue)
		}
	}

	grandBalance := grandExpected - grandCollected
	fmt.Fprintf(sb, "\n\n───────────────\n")
	fmt.Fprintf(sb, "*Grand total — rent*\n")
	fmt.Fprintf(sb, "Collected: KES %s\n", formatAmount(grandCollected))
	fmt.Fprintf(sb, "Expected:  KES %s\n", formatAmount(grandExpected))
	if grandBalance > 0 {
		fmt.Fprintf(sb, "Balance:   KES %s outstanding", formatAmount(grandBalance))
	} else {
		fmt.Fprintf(sb, "Balance:   fully collected ✓")
	}
	return sb.String()
}

// agentCmdTotal shows rent totals for one landlord then, if there are deposits,
// appends them after a hard visual separator so they are never conflated.
func (s *Service) agentCmdTotal(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if isNotFound(err) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord. Please try again."
	}
	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, landlord.ID, mk)
	if err != nil {
		return "Could not load totals. Please try again."
	}

	var collected, expected int
	for _, u := range units {
		expected += u.Unit.ExpectedRent
		collected += u.TotalPaid
	}

	month := time.Now().Format("January 2006")
	balance := expected - collected
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*%s — %s*\n", apartmentLabel(landlord), month)

	// ── Rent section ──────────────────────────────────────────────────────────
	fmt.Fprintf(sb, "\n*Rent collected*\n")
	fmt.Fprintf(sb, "Collected: KES %s\n", formatAmount(collected))
	fmt.Fprintf(sb, "Expected:  KES %s\n", formatAmount(expected))
	if balance > 0 {
		fmt.Fprintf(sb, "Balance:   KES %s outstanding", formatAmount(balance))
	} else {
		fmt.Fprintf(sb, "Balance:   fully collected ✓")
	}

	// ── Deposit section — hard separator ─────────────────────────────────────
	if s.deposits != nil {
		var totalHeld, totalOutstanding int
		for _, u := range units {
			ref := normaliseRef(u.Unit.UnitRef)
			dep, err := s.deposits.Get(ctx, landlord.ID, ref)
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

func (s *Service) agentCmdReceipt(ctx context.Context, a *models.Agent, name, rawRef string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}

	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, landlord.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %q not found under %s.", rawRef, landlord.Name)
	}

	mk := monthKey()
	rows, err := s.repo.GetPaymentHistory12(ctx, unit.ID)
	if err != nil || len(rows) == 0 {
		return fmt.Sprintf("No payment found for Unit %s this month.", unit.UnitRef)
	}

	r := rows[0]
	if r.MonthKey != mk {
		return fmt.Sprintf("No payment recorded for Unit %s in %s.",
			unit.UnitRef, time.Now().Format("January 2006"))
	}

	return fmt.Sprintf(
		"*Receipt — %s / Unit %s*\nTenant: %s\nAmount: KES %s\nMonth: %s\nStatus: %s",
		apartmentLabel(landlord), unit.UnitRef, unit.TenantName,
		formatAmount(r.AmountPaid), time.Now().Format("January 2006"), string(r.Status),
	)
}

// agentCmdHistory shows the last 3 months for a unit with a prominent arrears
// banner at the top — matching the landlord-facing cmdHistory behaviour.
func (s *Service) agentCmdHistory(ctx context.Context, a *models.Agent, name, rawRef string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if isNotFound(err) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}
	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, landlord.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %q not found under %s.", rawRef, landlord.Name)
	}

	rows, err := s.repo.GetPaymentHistory12(ctx, unit.ID)
	if err != nil {
		return "Could not load history. Please try again."
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No payment history for Unit %s.", unit.UnitRef)
	}

	limit := 3
	if len(rows) < limit {
		limit = len(rows)
	}
	rows = rows[:limit]

	// ── Arrears banner ────────────────────────────────────────────────────────
	var totalArrears, totalShortfall int
	for _, r := range rows {
		if r.Status == models.PaymentStatusPartial {
			owes := r.ExpectedRentSnapshot - r.AmountPaid
			if owes > 0 {
				totalShortfall += owes
			}
		}
		if r.ArrearsCarried > totalArrears {
			totalArrears = r.ArrearsCarried
		}
	}
	outstandingTotal := totalArrears
	if totalShortfall > outstandingTotal {
		outstandingTotal = totalShortfall
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Payment History — %s / Unit %s (%s)*\n",
		apartmentLabel(landlord), unit.UnitRef, unit.TenantName)

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

	if outstandingTotal > 0 {
		fmt.Fprintf(sb, "\n\nReply *HISTORY-EXT %s %s* for the full 12-month view.", landlord.Name, unit.UnitRef)
	}

	return sb.String()
}

// ── HISTORY-EXT ───────────────────────────────────────────────────────────────

func (s *Service) agentCmdHistoryExt(ctx context.Context, a *models.Agent, name, rawRef string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if isNotFound(err) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}
	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, landlord.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %q not found under %s.", rawRef, landlord.Name)
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
	fmt.Fprintf(sb, "*12-Month History — %s / Unit %s (%s)*\n",
		apartmentLabel(landlord), unit.UnitRef, unit.TenantName)

	if len(filtered) == 0 {
		fmt.Fprintf(sb, "\n✗ %s\n  Paid: KES 0 / KES %s\n  No payment recorded yet.",
			formatMonthKey(monthKey()), formatAmount(unit.ExpectedRent))
		return sb.String()
	}

	// ── Arrears banner across all 12 months ───────────────────────────────────
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
			icon, formatMonthKey(r.MonthKey),
			formatAmount(r.AmountPaid), formatAmount(r.ExpectedRentSnapshot))
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

	// ── Deposit block — hard separator ────────────────────────────────────────
	if s.deposits != nil {
		dep, depErr := s.deposits.Get(ctx, landlord.ID, ref)
		if depErr == nil && dep.Deposit.DepositExpected > 0 {
			fmt.Fprintf(sb, "\n\n───────────────\n")
			fmt.Fprintf(sb, "*Security deposit*\n")
			fmt.Fprintf(sb, "Expected: KES %s\n", formatAmount(dep.Deposit.DepositExpected))
			fmt.Fprintf(sb, "Paid:     KES %s", formatAmount(dep.Deposit.DepositPaid))
			if dep.Deposit.DepositRefunded > 0 {
				fmt.Fprintf(sb, "\nRefunded: KES %s", formatAmount(dep.Deposit.DepositRefunded))
			}
			fmt.Fprintf(sb, "\nStatus:   %s", depositStatusSummary(dep.Deposit))
		}
	}

	return sb.String()
}

func (s *Service) agentCmdHistoryExtAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	mk := monthKey()
	sb := &strings.Builder{}
	sb.WriteString("*12-Month Portfolio History*\n")

	for i := range landlords {
		l := &landlords[i]
		if l.SubscriptionStatus == models.StatusSuspended {
			fmt.Fprintf(sb, "\n✗ *%s* — suspended\n", apartmentLabel(l))
			continue
		}
		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil || len(units) == 0 {
			continue
		}

		label := apartmentLabel(l)
		fmt.Fprintf(sb, "\n*%s* (%d unit(s))\n", label, len(units))

		for _, us := range units {
			fmt.Fprintf(sb, "  Unit %s — %s\n", us.Unit.UnitRef, us.Unit.TenantName)

			rows, err := s.repo.GetPaymentHistory12(ctx, us.Unit.ID)
			if err != nil {
				fmt.Fprintf(sb, "    (history unavailable)\n")
				continue
			}

			startMonth := us.Unit.CreatedAt.Format("2006-01")
			count := 0
			for _, r := range rows {
				if r.MonthKey < startMonth {
					continue
				}
				icon := statusEmoji(r.Status)
				fmt.Fprintf(sb, "    %s %s: KES %s / KES %s",
					icon, formatMonthKey(r.MonthKey),
					formatAmount(r.AmountPaid), formatAmount(r.ExpectedRentSnapshot))
				if r.ArrearsCarried > 0 {
					fmt.Fprintf(sb, "\n      ↳ KES %s carried forward", formatAmount(r.ArrearsCarried))
				}
				sb.WriteString("\n")
				count++
				if count >= 6 {
					break
				}
			}
			if count == 0 {
				fmt.Fprintf(sb, "    No payments recorded yet.\n")
			}
		}
	}

	return sb.String()
}

func (s *Service) agentCmdLandlordHistory(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}

	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	rows, err := s.repo.GetPortfolioHistory12(ctx, landlord.ID)
	if err != nil {
		return "Could not load portfolio history. Please try again."
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No payment history recorded for %s.", landlord.Name)
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*12-Month Portfolio — %s*\n", apartmentLabel(landlord))

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

func (s *Service) agentCmdDeposit(ctx context.Context, a *models.Agent, name, rawRef string) string {
	if s.deposits == nil {
		return "Deposit tracking is not available. Please contact support."
	}

	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}

	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	ref := normaliseRef(rawRef)
	result, err := s.deposits.Get(ctx, landlord.ID, ref)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("Unit %q not found under %s.", rawRef, landlord.Name)
		}
		return "Could not load deposit info. Please try again."
	}

	dep := result.Deposit
	unit := result.Unit
	label := apartmentLabel(landlord)

	if dep.DepositExpected == 0 && dep.DepositPaid == 0 {
		return fmt.Sprintf("*Security deposit — %s / Unit %s*\n\nNo deposit on record.", label, unit.UnitRef)
	}

	held := dep.DepositPaid - dep.DepositRefunded
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Security deposit — %s / Unit %s*\n", label, unit.UnitRef)
	fmt.Fprintf(sb, "Tenant: %s\n\n", unit.TenantName)
	fmt.Fprintf(sb, "Expected deposit: KES %s\n", formatAmount(dep.DepositExpected))
	fmt.Fprintf(sb, "Total received:   KES %s\n", formatAmount(dep.DepositPaid))
	if dep.DepositRefunded > 0 {
		fmt.Fprintf(sb, "Total refunded:   KES %s\n", formatAmount(dep.DepositRefunded))
	}
	fmt.Fprintf(sb, "Held:             KES %s\n", formatAmount(held))
	fmt.Fprintf(sb, "Status:           %s", depositStatusSummary(dep))
	return sb.String()
}

func (s *Service) agentCmdDepositByLandlord(ctx context.Context, a *models.Agent, name string) string {
	if s.deposits == nil {
		return "Deposit tracking is not available. Please contact support."
	}

	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}

	if landlord.SubscriptionStatus == models.StatusSuspended {
		return suspendedMsg(landlord.ID)
	}

	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, landlord.ID, mk)
	if err != nil || len(units) == 0 {
		return fmt.Sprintf("No units found for %s.", landlord.Name)
	}

	label := apartmentLabel(landlord)
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Security deposits — %s*\n", label)

	for _, us := range units {
		ref := normaliseRef(us.Unit.UnitRef)
		dep, err := s.deposits.Get(ctx, landlord.ID, ref)
		if err != nil || (dep.Deposit.DepositPaid == 0 && dep.Deposit.DepositExpected == 0) {
			fmt.Fprintf(sb, "  %s — %s — No deposit\n", us.Unit.UnitRef, us.Unit.TenantName)
			continue
		}
		fmt.Fprintf(sb, "  %s — %s — %s\n",
			us.Unit.UnitRef, us.Unit.TenantName, depositStatusSummary(dep.Deposit))
	}

	return sb.String()
}

func (s *Service) agentCmdDepositAll(ctx context.Context, a *models.Agent) string {
	if s.deposits == nil {
		return "Deposit tracking is not available. Please contact support."
	}

	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	mk := monthKey()
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Security deposit overview — %s*\n", time.Now().Format("January 2006"))

	var grandHeld int

	for i := range landlords {
		l := &landlords[i]
		if l.SubscriptionStatus == models.StatusSuspended {
			fmt.Fprintf(sb, "\n✗ *%s* — suspended\n", apartmentLabel(l))
			continue
		}
		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil || len(units) == 0 {
			continue
		}

		label := apartmentLabel(l)
		fmt.Fprintf(sb, "\n*%s*\n", label)

		for _, us := range units {
			ref := normaliseRef(us.Unit.UnitRef)
			dep, err := s.deposits.Get(ctx, l.ID, ref)
			if err != nil || (dep.Deposit.DepositPaid == 0 && dep.Deposit.DepositExpected == 0) {
				fmt.Fprintf(sb, "  %s — %s — No deposit\n", us.Unit.UnitRef, us.Unit.TenantName)
				continue
			}
			d := dep.Deposit
			if d.DepositBalance > 0 {
				grandHeld += d.DepositBalance
			} else {
				grandHeld += d.DepositPaid - d.DepositRefunded
			}
			fmt.Fprintf(sb, "  %s — %s — %s\n",
				us.Unit.UnitRef, us.Unit.TenantName, depositStatusSummary(d))
		}
	}

	fmt.Fprintf(sb, "\n───────────────\n")
	fmt.Fprintf(sb, "*Total deposits held: KES %s*\n", formatAmount(grandHeld))
	fmt.Fprintf(sb, "_(security deposits — separate from rent)_")
	return sb.String()
}

// ── BILLING ───────────────────────────────────────────────────────────────────

func (s *Service) agentCmdBillingAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*RentLoop — Billing Status*\n")

	for i := range landlords {
		l := &landlords[i]
		label := apartmentLabel(l)
		var icon, note string
		switch l.SubscriptionStatus {
		case models.StatusActive:
			icon = "✓"
			if l.BillingCycleEnd != nil {
				note = fmt.Sprintf("active until %s", l.BillingCycleEnd.Format("02 Jan 2006"))
			} else {
				note = "active"
			}
		case models.StatusGrace:
			icon = "⚠"
			note = "grace period — payment due"
		case models.StatusSuspended:
			icon = "✗"
			note = "suspended"
		default:
			icon = "·"
			note = string(l.SubscriptionStatus)
		}
		fmt.Fprintf(sb, "\n%s *%s* — %s", icon, label, note)
		fmt.Fprintf(sb, "\n  Units: %d", l.UnitCount)
	}

	sb.WriteString("\n\nReply *PAY <name>* to trigger STK push for a landlord.")
	return sb.String()
}

func (s *Service) agentCmdBilling(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord. Please try again."
	}

	label := apartmentLabel(landlord)
	var statusLine string
	switch landlord.SubscriptionStatus {
	case models.StatusActive:
		if landlord.BillingCycleEnd != nil {
			statusLine = fmt.Sprintf("✓ Active until %s", landlord.BillingCycleEnd.Format("02 Jan 2006"))
		} else {
			statusLine = "✓ Active"
		}
	case models.StatusGrace:
		statusLine = "⚠ Grace period — payment overdue"
	case models.StatusSuspended:
		statusLine = "✗ Suspended"
	default:
		statusLine = string(landlord.SubscriptionStatus)
	}

	return fmt.Sprintf(
		"*Billing — %s*\n\nStatus: %s\nUnits: %d\n\nReply *PAY %s* to send STK push to the landlord.",
		label, statusLine, landlord.UnitCount, landlord.Name,
	)
}

// agentCmdPay triggers an STK push on behalf of a landlord.
// The agent provides the landlord name; the bot uses the landlord's
// own WhatsApp number as the M-Pesa target phone.
func (s *Service) agentCmdPay(ctx context.Context, agentPhone string, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord. Please try again."
	}

	// Normalise the landlord's WhatsApp phone to E.164 without +
	phone := normalisePhone(landlord.WhatsAppPhone)
	if phone == "" {
		return fmt.Sprintf("No valid phone on record for %s. Contact support.", landlord.Name)
	}

	body, _ := json.Marshal(map[string]string{
		"landlord_id": landlord.ID,
		"phone":       phone,
	})
	resp, err := http.Post(
		os.Getenv("INTERNAL_BASE_URL")+"/billing/stk",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil || (resp != nil && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent) {
		slog.Error("agent pay: stk trigger failed", "landlord_id", landlord.ID)
		return "Could not initiate payment. Please try again or contact support."
	}
	if resp.StatusCode == http.StatusNoContent {
		return fmt.Sprintf("%s is on the free tier — no payment required.", apartmentLabel(landlord))
	}

	return fmt.Sprintf(
		"✅ STK push sent to *%s* (%s).\nProperty: %s\nThey should enter their M-Pesa PIN to complete payment.",
		landlord.Name, landlord.WhatsAppPhone, apartmentLabel(landlord),
	)
}

// Avoids importing errors in every call site.
func isNotFound(err error) bool {
	return err != nil && (err.Error() == models.ErrNotFound.Error() ||
		err == models.ErrNotFound)
}

func agentHelpText() string {
	return "*RentLoop Agent Commands*\n\n" +
		"*CLIENTS* — list all your landlords\n" +
		"*LIST* — grouped view of all units across all landlords\n" +
		"*LIST Wanjiku* — full paid vs unpaid for one landlord\n" +
		"*REMIND ALL* — send reminders to all unpaid tenants\n" +
		"*REMIND Wanjiku* — send reminders for one landlord\n" +
		"*TOTAL ALL* — per-apartment rent with overdue counts\n" +
		"*TOTAL Wanjiku* — rent total for one landlord (deposits shown separately)\n" +
		"*RECEIPT Wanjiku A1* — receipt for a unit this month\n" +
		"*HISTORY Wanjiku A1* — last 3 months with arrears summary\n" +
		"*HISTORY-EXT Wanjiku A1* — 12-month history with arrears\n" +
		"*HISTORY-EXT ALL* — full portfolio 12-month history\n" +
		"*LANDLORD-HISTORY Wanjiku* — 12-month portfolio performance\n" +
		"*DEPOSIT Wanjiku A1* — deposit for one unit\n" +
		"*DEPOSIT STATUS ALL* — all deposits across portfolio\n" +
		"*DEPOSIT STATUS Wanjiku* — all deposits for one landlord\n" +
		"*BILLING ALL* — subscription status for all landlords\n" +
		"*BILLING Wanjiku* — billing detail for one landlord\n" +
		"*PAY Wanjiku* — trigger M-Pesa STK push for a landlord"

}
