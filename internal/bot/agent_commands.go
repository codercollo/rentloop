// Package bot implements agent-facing command handling for the WhatsApp bot.
//
// It parses agent commands (e.g. CLIENTS, LIST, TOTAL) and executes
// portfolio-level operations across multiple landlords, focusing on
// command routing, validation, and response formatting.
package bot

import (
	"context"
	"errors"
	"fmt"
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
			response = "Usage: LIST <landlord name>\nExample: LIST Wanjiku"
		} else {
			name := strings.Join(parts[1:], " ")
			response = s.agentCmdList(ctx, a, name)
		}
	case "REMIND":
		if len(parts) < 2 {
			response = "Usage: REMIND <landlord name>\nExample: REMIND Wanjiku"
		} else {
			name := strings.Join(parts[1:], " ")
			response = s.agentCmdRemind(ctx, a, name)
		}
	case "TOTAL":
		if len(parts) >= 2 && strings.ToUpper(parts[1]) == "ALL" {
			response = s.agentCmdTotalAll(ctx, a)
		} else if len(parts) >= 2 {
			name := strings.Join(parts[1:], " ")
			response = s.agentCmdTotal(ctx, a, name)
		} else {
			response = s.agentCmdTotalAll(ctx, a)
		}
	case "RECEIPT":
		// RECEIPT Wanjiku 4B
		if len(parts) < 3 {
			response = "Usage: RECEIPT <landlord name> <unit>\nExample: RECEIPT Wanjiku 4B"
		} else {
			unitRef := parts[len(parts)-1]
			name := strings.Join(parts[1:len(parts)-1], " ")
			response = s.agentCmdReceipt(ctx, a, name, unitRef)
		}
	case "HISTORY":
		// HISTORY Wanjiku 4B
		if len(parts) < 3 {
			response = "Usage: HISTORY <landlord name> <unit>\nExample: HISTORY Wanjiku 4B"
		} else {
			unitRef := parts[len(parts)-1]
			name := strings.Join(parts[1:len(parts)-1], " ")
			response = s.agentCmdHistory(ctx, a, name, unitRef)
		}
	case "HELP":
		response = agentHelpText()
	default:
		response = agentHelpText()
	}

	s.reply(ctx, from, response)
}

// agentCmdClients lists all landlords under this agent with their status.
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
	for _, l := range landlords {
		status := "✓"
		if l.SubscriptionStatus == models.StatusGrace {
			status = "⚠"
		} else if l.SubscriptionStatus == models.StatusSuspended {
			status = "✗"
		}
		name := l.ApartmentName
		if name == "" {
			name = l.Name
		}
		fmt.Fprintf(sb, "\n%s %s — %d units", status, name, l.UnitCount)
	}
	sb.WriteString("\n\nReply *LIST <name>* to check a client's payment status.")
	return sb.String()
}

// agentCmdList shows the paid vs unpaid breakdown for a specific landlord.
func (s *Service) agentCmdList(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q. Check the name and try again.", name)
		}
		return "Could not find landlord. Please try again."
	}

	mk := monthKey()
	units, err := s.repo.GetUnitsWithStatus(ctx, landlord.ID, mk)
	if err != nil {
		return "Could not load units. Please try again."
	}
	if len(units) == 0 {
		return fmt.Sprintf("%s has no units yet.", landlord.Name)
	}

	var paid, unpaid []string
	var totalPaid, totalExpected int

	for _, u := range units {
		totalExpected += u.Unit.ExpectedRent
		if u.IsPaid {
			totalPaid += u.Unit.ExpectedRent
			paid = append(paid, fmt.Sprintf("✓ %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
		} else {
			remaining := u.Unit.ExpectedRent - u.TotalPaid
			if u.TotalPaid > 0 {
				unpaid = append(unpaid, fmt.Sprintf("⚠ %s — %s (partial, KES %d left)", u.Unit.UnitRef, u.Unit.TenantName, remaining))
			} else {
				unpaid = append(unpaid, fmt.Sprintf("✗ %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
			}
		}
	}

	label := landlord.ApartmentName
	if label == "" {
		label = landlord.Name
	}
	month := time.Now().Format("January 2006")

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*%s — %s*\n", label, month)
	fmt.Fprintf(sb, "Paid: %d/%d units · KES %d\n", len(paid), len(units), totalPaid)
	if len(paid) > 0 {
		sb.WriteString("\n")
		sb.WriteString(strings.Join(paid, "\n"))
	}
	if len(unpaid) > 0 {
		sb.WriteString("\n\n")
		sb.WriteString(strings.Join(unpaid, "\n"))
		fmt.Fprintf(sb, "\n\nReply *REMIND %s* to nudge unpaid tenants.", landlord.Name)
	}
	return sb.String()
}

// agentCmdRemind sends reminders for unpaid tenants under a landlord.
func (s *Service) agentCmdRemind(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord. Please try again."
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
		if err := s.sms.SendReminder(ctx,
			u.Unit.TenantPhone, u.Unit.TenantName,
			u.Unit.UnitRef, u.Unit.ExpectedRent, month,
		); err == nil {
			reminded++
		} else {
			failed = append(failed, u.Unit.UnitRef)
		}
	}

	if reminded == 0 && len(failed) == 0 {
		return fmt.Sprintf("All of %s's units are paid for %s.", landlord.Name, month)
	}
	msg := fmt.Sprintf("Sent %d reminder(s) for %s.", reminded, landlord.Name)
	if len(failed) > 0 {
		msg += fmt.Sprintf("\nFailed to reach: %s", strings.Join(failed, ", "))
	}
	return msg
}

// agentCmdTotalAll shows combined totals across all managed landlords.
func (s *Service) agentCmdTotalAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
	}
	if len(landlords) == 0 {
		return "No landlords registered under your account yet."
	}

	mk := monthKey()
	var totalCollected, totalExpected int

	for _, l := range landlords {
		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil {
			continue
		}
		for _, u := range units {
			totalExpected += u.Unit.ExpectedRent
			totalCollected += u.TotalPaid
		}
	}

	month := time.Now().Format("January 2006")
	return fmt.Sprintf(
		"*RentLoop — Portfolio Total — %s*\n"+
			"Collected: KES %d\nExpected:  KES %d\nBalance:   KES %d\n"+
			"Landlords: %d",
		month, totalCollected, totalExpected, totalExpected-totalCollected, len(landlords),
	)
}

// agentCmdTotal shows totals for one specific landlord.
func (s *Service) agentCmdTotal(ctx context.Context, a *models.Agent, name string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord. Please try again."
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

	label := landlord.ApartmentName
	if label == "" {
		label = landlord.Name
	}
	month := time.Now().Format("January 2006")
	return fmt.Sprintf(
		"*%s — %s*\nCollected: KES %d\nExpected:  KES %d\nBalance:   KES %d",
		label, month, collected, expected, expected-collected,
	)
}

// agentCmdReceipt resends a receipt for a unit under a specific landlord.
func (s *Service) agentCmdReceipt(ctx context.Context, a *models.Agent, name, rawRef string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}

	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, landlord.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found under %s.", rawRef, landlord.Name)
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

	label := landlord.ApartmentName
	if label == "" {
		label = landlord.Name
	}
	return fmt.Sprintf(
		"*Receipt — %s / Unit %s*\nTenant: %s\nAmount: KES %d\nMonth: %s\nStatus: %s",
		label, unit.UnitRef, unit.TenantName, h.Amount,
		time.Now().Format("January 2006"), h.Status,
	)
}

// agentCmdHistory returns the last 3 months of payments for a unit.
func (s *Service) agentCmdHistory(ctx context.Context, a *models.Agent, name, rawRef string) string {
	landlord, err := s.repo.GetLandlordByAgentAndName(ctx, a.ID, name)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return fmt.Sprintf("No landlord found matching %q.", name)
		}
		return "Could not find landlord."
	}

	ref := normaliseRef(rawRef)
	unit, err := s.repo.GetUnitByRef(ctx, landlord.ID, ref)
	if err != nil {
		return fmt.Sprintf("Unit %s not found under %s.", rawRef, landlord.Name)
	}

	history, err := s.repo.GetPaymentHistory(ctx, unit.ID, 3)
	if err != nil {
		return "Could not load history. Please try again."
	}
	if len(history) == 0 {
		return fmt.Sprintf("No payment history found for Unit %s.", unit.UnitRef)
	}

	label := landlord.ApartmentName
	if label == "" {
		label = landlord.Name
	}
	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*Payment History — %s / Unit %s (%s)*\n", label, unit.UnitRef, unit.TenantName)
	for _, h := range history {
		fmt.Fprintf(sb, "\n%s — KES %d (%s)", h.MonthKey, h.Amount, h.Status)
	}
	return sb.String()
}

// agentHelpText returns the agent command reference.
func agentHelpText() string {
	return "*RentLoop Agent Commands*\n\n" +
		"*CLIENTS* — list all your landlords\n" +
		"*LIST Wanjiku* — full paid vs unpaid for a landlord\n" +
		"*REMIND Wanjiku* — send reminders for a landlord\n" +
		"*TOTAL ALL* — combined total across all landlords\n" +
		"*TOTAL Wanjiku* — total for one landlord\n" +
		"*RECEIPT Wanjiku 4B* — receipt for a unit this month\n" +
		"*HISTORY Wanjiku 4B* — last 3 months for a unit"
}
