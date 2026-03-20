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
	default:
		response = agentHelpText()
	}

	s.reply(ctx, from, response)
}

// agentCmdClients lists all landlords under this agent.
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
		status := ""
		if l.SubscriptionStatus == models.StatusGrace {
			status = " ⚠"
		} else if l.SubscriptionStatus == models.StatusSuspended {
			status = " ✗"
		}
		fmt.Fprintf(sb, "\n• %s — %d units%s", l.Name, l.UnitCount, status)
	}
	return sb.String()
}

// agentCmdList shows paid vs unpaid for a specific landlord.
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

	var paidCount int
	var collected int
	for _, u := range units {
		if u.IsPaid {
			paidCount++
			collected += u.Unit.ExpectedRent
		}
	}

	month := time.Now().Format("January 2006")
	return fmt.Sprintf(
		"*%s — %s*\nPaid: %d/%d units · KES %d collected\n\n"+
			"Reply *LIST %s* for full breakdown.",
		landlord.Name, month,
		paidCount, len(units), collected, landlord.Name,
	)
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
	for _, u := range units {
		if u.IsPaid {
			continue
		}
		if err := s.sms.SendReminder(ctx,
			u.Unit.TenantPhone, u.Unit.TenantName,
			u.Unit.UnitRef, u.Unit.ExpectedRent, month,
		); err == nil {
			reminded++
		}
	}

	if reminded == 0 {
		return fmt.Sprintf("All of %s's units are paid for %s.", landlord.Name, month)
	}
	return fmt.Sprintf("Sent %d reminder(s) for %s.", reminded, landlord.Name)
}

// agentCmdTotalAll shows combined totals across all managed landlords.
func (s *Service) agentCmdTotalAll(ctx context.Context, a *models.Agent) string {
	landlords, err := s.repo.GetLandlordsByAgent(ctx, a.ID)
	if err != nil {
		return "Could not load clients. Please try again."
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

	month := time.Now().Format("January 2006")
	return fmt.Sprintf(
		"*%s — %s*\nCollected: KES %d\nExpected:  KES %d\nBalance:   KES %d",
		landlord.Name, month, collected, expected, expected-collected,
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

	history, err := s.repo.GetPaymentHistory(ctx, unit.ID, 1)
	if err != nil || len(history) == 0 {
		return fmt.Sprintf("No payment found for Unit %s this month.", unit.UnitRef)
	}

	h := history[0]
	return fmt.Sprintf(
		"*Receipt — %s / Unit %s*\nTenant: %s\nAmount: KES %d\nStatus: %s",
		landlord.Name, unit.UnitRef, unit.TenantName, h.Amount, h.Status,
	)
}

// agentHelpText returns the agent command reference.
func agentHelpText() string {
	return "*RentLoop Agent Commands*\n\n" +
		"*CLIENTS* — list all your landlords\n" +
		"*LIST Wanjiku* — paid vs unpaid for a landlord\n" +
		"*REMIND Wanjiku* — send reminders for a landlord\n" +
		"*TOTAL ALL* — combined total across all landlords\n" +
		"*TOTAL Wanjiku* — total for one landlord\n" +
		"*RECEIPT Wanjiku 4B* — resend receipt for a unit"
}
