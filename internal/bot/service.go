// Package bot implements the WhatsApp bot service layer for RentLoop.
//
// It orchestrates all conversational workflows between users (landlords and agents)
// and the system, acting as the bridge between inbound messages and business logic.
//
// Responsibilities include:
//
//   - Identifying the sender (landlord or agent) via phone number
//   - Routing and handling bot commands based on user role
//   - Enforcing subscription rules (active, grace, suspended)
//   - Coordinating data access through the BotRepository interface
//   - Sending outbound WhatsApp messages via the Sender interface
//   - Triggering SMS reminders to tenants via the SMSNotifier interface
//   - Supporting payment matching and unit management workflows
//
// The package follows a service-oriented design where the Service struct
// encapsulates all command handling logic, while dependencies (repository,
// messaging, notifications) are injected via interfaces for flexibility
// and testability.
//
// This layer is intentionally decoupled from transport (HTTP/webhooks)
// and persistence implementations, making it easy to extend, test, and evolve.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codercollo/rentloop/internal/matcher"
	"github.com/codercollo/rentloop/internal/models"
)

// Sender sends outbound WhatsApp messages.
type Sender interface {
	Send(ctx context.Context, to, message string) error
}

// SMSNotifier sends reminder SMS to tenants.
type SMSNotifier interface {
	SendReminder(ctx context.Context, phone, tenantName, unitRef string, expectedRent int, month string) error
}

// BotRepository is the persistence interface the service depends on.
type BotRepository interface {
	GetLandlordByPhone(ctx context.Context, phone string) (*models.Landlord, error)
	GetAgentByPhone(ctx context.Context, phone string) (*models.Agent, error)
	GetUnitsWithStatus(ctx context.Context, landlordID, monthKey string) ([]UnitStatus, error)
	GetLandlordsByAgent(ctx context.Context, agentID string) ([]models.Landlord, error)
	GetLandlordByAgentAndName(ctx context.Context, agentID, name string) (*models.Landlord, error)
	GetPaymentHistory(ctx context.Context, unitID string, months int) ([]PaymentHistoryRow, error)
	GetUnitByRef(ctx context.Context, landlordID, ref string) (*models.Unit, error)
	GetUnmatchedPayment(ctx context.Context, landlordID, transactionID string) (*models.Payment, error)
	AssignPaymentToUnit(ctx context.Context, paymentID, unitID string) error
	InsertUnit(ctx context.Context, u models.Unit) (*models.Unit, error)
	GetLandlordsWithUnpaid(ctx context.Context, monthKey string) ([]models.Landlord, error)
}

// Service orchestrates all bot command logic.
type Service struct {
	repo   BotRepository
	sender Sender
	sms    SMSNotifier
}

// NewService wires all dependencies.
func NewService(repo BotRepository, sender Sender, sms SMSNotifier) *Service {
	return &Service{repo: repo, sender: sender, sms: sms}
}

// Handle is the main dispatch entry point called by the HTTP handler.
// It identifies the sender, checks subscription, and routes the command.
func (s *Service) Handle(ctx context.Context, from, text string) {
	upper := strings.ToUpper(strings.TrimSpace(text))

	// Identify sender role
	landlord, agent, err := s.identify(ctx, from)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			s.reply(ctx, from,
				"Welcome to RentLoop.\n\n"+
					"Send *JOIN* to register as a landlord, or contact your property manager.")
			return
		}
		slog.Error("bot: identify sender failed", "from", from, "error", err)
		s.reply(ctx, from, "Something went wrong. Please try again in a moment.")
		return
	}

	// Agent path
	if agent != nil {
		s.handleAgent(ctx, from, upper, agent)
		return
	}

	// Landlord path — check subscription first
	if landlord.SubscriptionStatus == models.StatusSuspended {
		amount := landlord.UnitCount * 50
		s.reply(ctx, from, fmt.Sprintf(
			"*RentLoop — Account Suspended*\n\n"+
				"Your subscription has lapsed.\n"+
				"Pay KES %d to Paybill %s, account: RENTLOOP-%s\n\n"+
				"Your payment history is safe and will be restored on payment.",
			amount, landlord.PaybillNumber, shortID(landlord.ID),
		))
		return
	}

	// Grace period — process command but append warning
	graceWarning := ""
	if landlord.SubscriptionStatus == models.StatusGrace {
		amount := landlord.UnitCount * 50
		graceWarning = fmt.Sprintf(
			"\n\n_⚠ Subscription due: KES %d. Pay to Paybill %s, ref: RENTLOOP-%s_",
			amount, landlord.PaybillNumber, shortID(landlord.ID),
		)
	}

	response := s.handleLandlord(ctx, upper, landlord)
	s.reply(ctx, from, response+graceWarning)
}

// identify returns the landlord or agent for the given phone number.
// Exactly one of (landlord, agent) will be non-nil on success.
func (s *Service) identify(ctx context.Context, phone string) (*models.Landlord, *models.Agent, error) {
	agent, err := s.repo.GetAgentByPhone(ctx, phone)
	if err == nil {
		return nil, agent, nil
	}
	if !errors.Is(err, models.ErrNotFound) {
		return nil, nil, err
	}

	landlord, err := s.repo.GetLandlordByPhone(ctx, phone)
	if err != nil {
		return nil, nil, err
	}
	return landlord, nil, nil
}

// monthKey returns the current YYYY-MM key in EAT.
func monthKey() string {
	eat, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		eat = time.FixedZone("EAT", 3*60*60)
	}
	return time.Now().In(eat).Format("2006-01")
}

// reply sends a WhatsApp response and logs any failure.
func (s *Service) reply(ctx context.Context, to, message string) {
	if err := s.sender.Send(ctx, to, message); err != nil {
		slog.Error("bot: reply failed", "to", to, "error", err)
	}
}

// normaliseRef applies the same normalisation rules as the matcher package.
func normaliseRef(raw string) string {
	return matcher.Normalise(raw)
}

// shortID safely trims an ID to 8 characters to avoid slice panics.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
