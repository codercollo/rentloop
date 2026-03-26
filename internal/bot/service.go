// Package bot implements the WhatsApp bot service layer for RentLoop.
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

// SMSNotifier sends reminder and onboarding SMS to tenants.
type SMSNotifier interface {
	SendReminder(ctx context.Context, phone, tenantName, unitRef string, expectedRent int, month string) error
	SendOnboarding(ctx context.Context, phone, tenantName, unitRef, paybill, apartmentName string, expectedRent int) error
}

// OnboardingService is the subset of onboarding.Service that bot needs.
// Using an interface here avoids a circular import:
//
//	bot/service.go → onboarding (concrete) → bot/repository.go (already imports bot)
//
// The interface is satisfied implicitly by *onboarding.Service.
type OnboardingService interface {
	IsPendingApartmentName(phone string) bool
	HandleJoin(ctx context.Context, from string)
	HandleApartmentName(ctx context.Context, from, apartmentName string)
	HandleBulkAdd(ctx context.Context, from, fileURL string)
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
	UpdateExpectedRent(ctx context.Context, landlordID, unitRef string, rent int) error
	ReplaceUnitTenant(ctx context.Context, landlordID string, u models.Unit) (*models.Unit, error)
	InsertManualPayment(ctx context.Context, p models.Payment) (*models.Payment, error)
}

// Service orchestrates all bot command logic.
type Service struct {
	repo       BotRepository
	sender     Sender
	sms        SMSNotifier
	onboarding OnboardingService
}

// NewService wires all dependencies.
func NewService(repo BotRepository, sender Sender, sms SMSNotifier) *Service {
	return &Service{repo: repo, sender: sender, sms: sms}
}

// SetOnboarding injects the onboarding service after construction.
// Called from main.go after both services are initialised.
// Accepts OnboardingService interface — *onboarding.Service satisfies it implicitly.
func (s *Service) SetOnboarding(svc OnboardingService) {
	s.onboarding = svc
}

// Handle is the main dispatch entry point called by the HTTP handler.
func (s *Service) Handle(ctx context.Context, from, text string) {
	// ── Onboarding gate — must be checked BEFORE identify() ──────────────
	// If this phone is mid-JOIN flow waiting to supply their apartment name,
	// route their reply directly to onboarding. Do not attempt command parsing
	// and do not hit the DB to identify them — they aren't registered yet.
	if s.onboarding != nil && s.onboarding.IsPendingApartmentName(from) {
		s.onboarding.HandleApartmentName(ctx, from, text)
		return
	}

	upper := strings.ToUpper(strings.TrimSpace(text))

	landlord, agent, err := s.identify(ctx, from)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			// Unknown sender — only valid action is JOIN
			if upper == "JOIN" {
				if s.onboarding != nil {
					s.onboarding.HandleJoin(ctx, from)
				} else {
					s.reply(ctx, from, "Registration is not available right now. Please try again later.")
				}
				return
			}
			s.reply(ctx, from,
				"Welcome to RentLoop.\n\n"+
					"Send *JOIN* to register as a landlord, or contact your property manager.")
			return
		}
		slog.Error("bot: identify sender failed", "from", from, "error", err)
		s.reply(ctx, from, "Something went wrong. Please try again in a moment.")
		return
	}

	if agent != nil {
		s.handleAgent(ctx, from, upper, agent)
		return
	}

	// Suspended — full lockout, show payment instructions only
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

	// Grace — process command but append billing warning
	graceWarning := ""
	if landlord.SubscriptionStatus == models.StatusGrace {
		amount := landlord.UnitCount * 50
		graceWarning = fmt.Sprintf(
			"\n\n_⚠ Subscription due: KES %d. Pay to Paybill %s, ref: RENTLOOP-%s_",
			amount, landlord.PaybillNumber, shortID(landlord.ID),
		)
	}

	response := s.handleLandlord(ctx, upper, landlord)
	if response != "" {
		s.reply(ctx, from, response+graceWarning)
	}
}

// identify returns the landlord or agent for the given phone number.
// Agent lookup runs first — an agent phone never collides with a landlord phone.
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

// monthKey returns the current YYYY-MM string in Africa/Nairobi time.
func monthKey() string {
	eat, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		eat = time.FixedZone("EAT", 3*60*60)
	}
	return time.Now().In(eat).Format("2006-01")
}

// shortID returns the first 8 chars of a UUID safely.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
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

// HandleSMS is the SMS entry point. It normalises the phone number
// then delegates to the same command pipeline as WhatsApp.
func (s *Service) HandleSMS(ctx context.Context, from, text string) {
	from = normaliseSMSPhone(from)
	s.Handle(ctx, from, text)
}

// normaliseSMSPhone converts 07XXXXXXXX → +254XXXXXXXX so the DB
// lookup finds the same landlord regardless of which channel they use.
func normaliseSMSPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	if strings.HasPrefix(phone, "07") || strings.HasPrefix(phone, "01") {
		return "+254" + phone[1:]
	}
	if strings.HasPrefix(phone, "254") && !strings.HasPrefix(phone, "+") {
		return "+" + phone
	}
	return phone
}
