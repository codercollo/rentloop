// Package bot implements the WhatsApp bot service layer.
// It routes inbound messages to the correct handler (landlord, agent, or onboarding)
// and owns the daily digest cron job.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/codercollo/rentloop/internal/billing"
	deposits "github.com/codercollo/rentloop/internal/deposit"
	"github.com/codercollo/rentloop/internal/models"
)

// BotRepository is the full persistence interface for the bot service.
// One concrete implementation satisfies all methods; tests inject a mock.
type BotRepository interface {
	// ── Landlord / agent resolution ───────────────────────────────────────────
	GetLandlordByPhone(ctx context.Context, phone string) (*models.Landlord, error)
	GetAgentByPhone(ctx context.Context, phone string) (*models.Agent, error)
	GetLandlordsByAgent(ctx context.Context, agentID string) ([]models.Landlord, error)
	GetLandlordByAgentAndName(ctx context.Context, agentID, name string) (*models.Landlord, error)

	// ── Unit operations ───────────────────────────────────────────────────────
	GetUnitsWithStatus(ctx context.Context, landlordID, monthKey string) ([]UnitStatus, error)
	GetUnitByRef(ctx context.Context, landlordID, normalisedRef string) (*models.Unit, error)
	InsertUnit(ctx context.Context, u models.Unit) (*models.Unit, error)
	ReplaceUnitTenant(ctx context.Context, landlordID string, u models.Unit) (*models.Unit, error)
	UpdateExpectedRent(ctx context.Context, landlordID, normalisedRef string, rent int) error
	UpdateUnitCount(ctx context.Context, landlordID string, count int) error

	// ── Payment operations ────────────────────────────────────────────────────
	GetPaymentHistory(ctx context.Context, unitID string, limit int) ([]PaymentHistoryRow, error)
	InsertManualPayment(ctx context.Context, p models.Payment) (*models.Payment, error)
	GetUnmatchedPayment(ctx context.Context, landlordID, transactionID string) (*models.Payment, error)

	// FIX 4: signature extended with expectedRent and amount so the repo can
	// compute the correct status (paid / partial / overpaid) instead of always
	// writing status='paid' regardless of amount.
	AssignPaymentToUnit(ctx context.Context, paymentID, unitID string, expectedRent, amount int) error

	// ── Phase 1: 12-month history ─────────────────────────────────────────────
	GetPaymentHistory12(ctx context.Context, unitID string) ([]models.MonthlyPaymentRow, error)
	GetPortfolioHistory12(ctx context.Context, landlordID string) ([]models.PortfolioMonthRow, error)

	// ── Digest ────────────────────────────────────────────────────────────────
	GetLandlordsWithUnpaid(ctx context.Context, monthKey string) ([]models.Landlord, error)
}

// SMSSender sends SMS messages to tenants.
// SMSSender sends SMS/WhatsApp reminders to tenants.
type SMSSender interface {
	SendReminder(ctx context.Context, phone, tenantName, unitRef string, remaining, alreadyPaid int, month string) error
}

// WASender sends WhatsApp messages.
type WASender interface {
	Send(ctx context.Context, to, message string) error
}

// OnboardingService handles the JOIN flow and CSV uploads.
type OnboardingService interface {
	HandleJoin(ctx context.Context, from string)
	HandleApartmentName(ctx context.Context, from, body string)
	HandleBulkAdd(ctx context.Context, from, fileURL string)
	IsPendingApartmentName(phone string) bool
}

// DepositService handles deposit tracking.
type DepositService interface {
	Get(ctx context.Context, landlordID, normalisedRef string) (*deposits.DepositResult, error)
	Receive(ctx context.Context, landlordID, normalisedRef string, amount int, note, recordedBy string) (*deposits.DepositResult, error)
	Refund(ctx context.Context, landlordID, normalisedRef string, amount int, note, recordedBy string) (*deposits.DepositResult, error)
}

// Service is the central bot service that handles all inbound WhatsApp messages.
type Service struct {
	repo       BotRepository
	wa         WASender
	sms        SMSSender
	onboarding OnboardingService
	deposits   DepositService
	payState   sync.Map
}

// NewService wires the mandatory dependencies.
func NewService(repo BotRepository, wa WASender, sms SMSSender) *Service {
	return &Service{
		repo: repo,
		wa:   wa,
		sms:  sms,
	}
}

// SetOnboarding injects the onboarding service after construction.
func (s *Service) SetOnboarding(o OnboardingService) {
	s.onboarding = o
}

// SetDeposits injects the deposit service after construction.
func (s *Service) SetDeposits(d DepositService) {
	s.deposits = d
}

// Handle is the entry point for every inbound WhatsApp message.
func (s *Service) Handle(ctx context.Context, from, text string) {
	upper := strings.ToUpper(strings.TrimSpace(text))

	// ── PAY flow ──────────────────────────────────────────────────────────────
	if upper == "PAY" {
		s.payState.Store(from, true)
		s.reply(ctx, from,
			"To renew your RentLoop subscription, reply with your M-Pesa phone number.\n"+
				"Example: *0712345678*")
		return
	}

	if _, ok := s.payState.Load(from); ok {
		s.payState.Delete(from)
		s.handlePayPhone(ctx, from, strings.TrimSpace(text))
		return
	}

	// ── Agent routing ─────────────────────────────────────────────────────────
	agent, err := s.repo.GetAgentByPhone(ctx, from)
	if err == nil && agent != nil {
		s.handleAgent(ctx, from, upper, agent)
		return
	}

	// ── Landlord routing ──────────────────────────────────────────────────────
	landlord, err := s.repo.GetLandlordByPhone(ctx, from)
	if err != nil || landlord == nil {
		s.reply(ctx, from,
			"Welcome to RentLoop! Send *JOIN* to register your property and get started.")
		return
	}

	resp := s.handleLandlord(ctx, upper, landlord)

	if landlord.SubscriptionStatus == models.StatusGrace {
		ref := billing.SubscriptionRef(landlord.ID)
		warning := "\n\n_Subscription due. Pay via Paybill *400200* · Acc *" +
			ref + "* to avoid suspension._"
		if resp != "" {
			resp += warning
		}
	}

	if resp != "" {
		s.reply(ctx, from, resp)
	}
}

// HandleSMS is the entry point for inbound SMS messages.
func (s *Service) HandleSMS(ctx context.Context, from, text string) {
	s.Handle(ctx, from, text)
}

func (s *Service) reply(ctx context.Context, to, message string) {
	if err := s.wa.Send(ctx, to, message); err != nil {
		slog.Error("bot: reply failed", "to", to, "error", err)
	}
}

// monthKey returns the current YYYY-MM string (Nairobi local time).
func monthKey() string {
	loc, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		return time.Now().Format("2006-01")
	}
	return time.Now().In(loc).Format("2006-01")
}

// normaliseRef delegates to the matcher package's Normalise function.
func normaliseRef(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ToUpper(s)
	for _, prefix := range []string{"APARTMENT", "FLAT", "HOUSE", "ROOM", "PLOT", "UNIT", "APT"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			s = strings.TrimSpace(s)
			break
		}
	}
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '_' || r == '/' || r == '.' {
			return -1
		}
		return r
	}, s)
	return s
}

func (s *Service) handlePayPhone(ctx context.Context, from, phone string) {
	// Normalise phone to 254XXXXXXXXX format.
	e164 := normalisePhone(phone)
	if e164 == "" {
		s.reply(ctx, from,
			"That doesn't look like a valid phone number. Please try again.\nExample: *0712345678*")
		return
	}

	landlord, err := s.repo.GetLandlordByPhone(ctx, from)
	if err != nil {
		s.reply(ctx, from, "Could not find your account. Please contact support.")
		return
	}

	body, _ := json.Marshal(map[string]string{
		"landlord_id": landlord.ID,
		"phone":       e164,
	})
	resp, err := http.Post(
		os.Getenv("INTERNAL_BASE_URL")+"/billing/stk",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil || resp.StatusCode != http.StatusOK {
		s.reply(ctx, from, "Could not initiate payment. Please try again or contact support.")
		return
	}

	s.reply(ctx, from,
		"✅ Check your phone and enter your M-Pesa PIN to complete payment.\n"+
			"Your account will be reactivated automatically once confirmed.")
}

// normalisePhone converts 07XXXXXXXX or +254XXXXXXXXX to 254XXXXXXXXX.
func normalisePhone(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.ReplaceAll(p, " ", "")
	p = strings.TrimPrefix(p, "+")
	if strings.HasPrefix(p, "07") && len(p) == 10 {
		return "254" + p[1:]
	}
	if strings.HasPrefix(p, "01") && len(p) == 10 {
		return "254" + p[1:]
	}
	if strings.HasPrefix(p, "254") && len(p) == 12 {
		return p
	}
	return ""
}
