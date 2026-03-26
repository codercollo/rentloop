// Package onboarding handles landlord onboarding via WhatsApp, including
// registration, CSV bulk uploads, validation, persistence, and SMS notifications.
// It coordinates Repository, Sender, and SMSNotifier and manages a simple
// in-memory state for users mid-registration.
package onboarding

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/codercollo/rentloop/internal/models"
)

// Repository is the persistence interface onboarding depends on.
type Repository interface {
	CreateLandlord(ctx context.Context, l models.Landlord) (*models.Landlord, error)
	GetLandlordByPhone(ctx context.Context, phone string) (*models.Landlord, error)
	InsertUnit(ctx context.Context, u models.Unit) (*models.Unit, error)
	UpdateUnitCount(ctx context.Context, landlordID string, count int) error
}

// Sender sends outbound WhatsApp messages.
type Sender interface {
	Send(ctx context.Context, to, message string) error
}

// SMSNotifier sends onboarding SMS to tenants.
type SMSNotifier interface {
	SendOnboarding(ctx context.Context, phone, tenantName, unitRef, paybill, apartmentName string, expectedRent int) error
}

// pendingState tracks landlords who have sent JOIN but not yet
// replied with their apartment name.
type pendingState struct {
	startedAt time.Time
}

// knownCommands is the set of bot commands we detect mid-onboarding
// to avoid treating a mistyped command as an apartment name.
var knownCommands = []string{
	"LIST", "REMIND", "TOTAL", "HELP", "MARK",
	"ADD", "CLAIM", "RECEIPT", "HISTORY", "REPLACE",
	"SET", "BULK", "JOIN",
}

// Service handles all onboarding logic.
type Service struct {
	repo       Repository
	sender     Sender
	sms        SMSNotifier
	ownerPhone string
	mu         sync.Mutex
	pending    map[string]pendingState
}

// NewService wires all dependencies.
func NewService(repo Repository, sender Sender, sms SMSNotifier, ownerPhone string) *Service {
	return &Service{
		repo:       repo,
		sender:     sender,
		sms:        sms,
		ownerPhone: ownerPhone,
		pending:    make(map[string]pendingState),
	}
}

// IsPendingApartmentName reports whether this phone is mid-JOIN,
// waiting to supply their apartment name.
func (s *Service) IsPendingApartmentName(phone string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.pending[phone]
	if !ok {
		return false
	}
	// Expire after 10 minutes of inactivity
	if time.Since(state.startedAt) > 10*time.Minute {
		delete(s.pending, phone)
		return false
	}
	return true
}

// HandleJoin processes the JOIN command from a new landlord.
// If already registered, sends the CSV template.
// Otherwise prompts them for their apartment name.
func (s *Service) HandleJoin(ctx context.Context, from string) {
	existing, err := s.repo.GetLandlordByPhone(ctx, from)
	if err == nil && existing != nil {
		s.sendTemplate(ctx, from, existing.PaybillNumber, existing.ApartmentName)
		return
	}

	// Mark this phone as pending apartment name
	s.mu.Lock()
	s.pending[from] = pendingState{startedAt: time.Now()}
	s.mu.Unlock()

	s.send(ctx, from,
		"*Welcome to RentLoop!* 🎉\n\n"+
			"Let's set up your account.\n\n"+
			"What is the name of your apartment or property?\n\n"+
			"_Example: Sunrise Apartments, Kilimani Court, Green Valley_",
	)
}

// HandleApartmentName completes registration with the apartment name
// supplied by the landlord in their follow-up message.
func (s *Service) HandleApartmentName(ctx context.Context, from, apartmentName string) {
	s.mu.Lock()
	delete(s.pending, from)
	s.mu.Unlock()

	// ── Command-detection guard ───────────────────────────────────────────
	// If the landlord typed a bot command instead of their apartment name,
	// remind them what we need and keep them in the pending state.
	// Without this, "LIST" would be registered as an apartment name.
	upperBody := strings.ToUpper(strings.TrimSpace(apartmentName))
	for _, cmd := range knownCommands {
		if strings.HasPrefix(upperBody, cmd) {
			s.send(ctx, from,
				"Please send the name of your apartment to complete registration.\n\n"+
					"_Example: Sunrise Apartments, Kilimani Court_",
			)
			s.mu.Lock()
			s.pending[from] = pendingState{startedAt: time.Now()}
			s.mu.Unlock()
			return
		}
	}

	// ── Empty input guard ─────────────────────────────────────────────────
	apartmentName = strings.TrimSpace(apartmentName)
	if apartmentName == "" {
		s.send(ctx, from, "Please send the name of your apartment to continue registration.")
		s.mu.Lock()
		s.pending[from] = pendingState{startedAt: time.Now()}
		s.mu.Unlock()
		return
	}

	landlord := models.Landlord{
		WhatsAppPhone:      from,
		Name:               "Landlord",
		ApartmentName:      apartmentName,
		PaybillNumber:      "174379", // default sandbox paybill — updated after KYC
		SubscriptionStatus: models.StatusActive,
	}

	created, err := s.repo.CreateLandlord(ctx, landlord)
	if err != nil {
		slog.Error("onboarding: create landlord failed", "phone", from, "error", err)
		s.send(ctx, from, "Registration failed. Please try again or contact support.")
		return
	}

	slog.Info("onboarding: landlord registered",
		"id", created.ID,
		"phone", from,
		"apartment", created.ApartmentName,
	)

	// Alert owner of new signup so they can follow up for Daraja credentials.
	if s.ownerPhone != "" {
		alert := fmt.Sprintf(
			"*New RentLoop signup* 🔔\n\n"+
				"Apartment: %s\n"+
				"Phone: %s\n"+
				"ID: %s\n\n"+
				"Follow up for Daraja credentials and paybill shortcode.",
			created.ApartmentName,
			created.WhatsAppPhone,
			shortID(created.ID),
		)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.send(ctx, s.ownerPhone, alert)
		}()
	}

	welcome := fmt.Sprintf(
		"*%s is now on RentLoop!* 🎉\n\n"+
			"Tenants pay to Paybill *%s* using their unit ref as the account number.\n\n"+
			"*To add your tenants, send a CSV file with these columns:*\n"+
			"unit, name, phone, rent\n\n"+
			"*Example:*\n"+
			"4B, John Kamau, 0712345678, 12500\n"+
			"2A, Mary Wanjiku, 0723456789, 8000\n\n"+
			"Or add one unit at a time:\n"+
			"*ADD UNIT 4B John Kamau 0712345678 12500*\n\n"+
			"Reply *HELP* for all commands.",
		created.ApartmentName,
		created.PaybillNumber,
	)

	s.send(ctx, from, welcome)
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// HandleBulkAdd processes a CSV file attachment sent via WhatsApp.
func (s *Service) HandleBulkAdd(ctx context.Context, from, fileURL string) {
	landlord, err := s.repo.GetLandlordByPhone(ctx, from)
	if err != nil {
		s.send(ctx, from, "You are not registered yet. Send *JOIN* to get started.")
		return
	}

	if fileURL == "" {
		s.sendTemplate(ctx, from, landlord.PaybillNumber, landlord.ApartmentName)
		return
	}

	slog.Info("onboarding: processing CSV", "landlord_id", landlord.ID, "url", fileURL)

	result, err := ParseCSV(fileURL)
	if err != nil {
		slog.Error("onboarding: parse csv failed", "error", err)
		s.send(ctx, from, "Could not read your file. Make sure it is a CSV and try again.")
		return
	}

	if len(result.Valid) == 0 && len(result.Errors) > 0 {
		s.send(ctx, from, "No valid rows found.\n\n"+result.FormatErrors())
		return
	}

	var inserted, skipped int
	for _, unit := range result.ToUnits(landlord.ID) {
		_, err := s.repo.InsertUnit(ctx, unit)
		if err != nil {
			skipped++
			slog.Warn("onboarding: insert unit skipped", "ref", unit.UnitRef, "error", err)
			continue
		}
		inserted++

		go func(u models.Unit) {
			smsCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.sms.SendOnboarding(
				smsCtx,
				u.TenantPhone,
				u.TenantName,
				u.UnitRef,
				landlord.PaybillNumber,
				landlord.ApartmentName,
				u.ExpectedRent,
			); err != nil {
				slog.Error("onboarding: tenant SMS failed", "unit", u.UnitRef, "error", err)
			}
		}(unit)
	}

	if inserted > 0 {
		if err := s.repo.UpdateUnitCount(ctx, landlord.ID, inserted); err != nil {
			slog.Error("onboarding: update unit count failed", "error", err)
		}
	}

	sb := &strings.Builder{}
	fmt.Fprintf(sb, "*%s — Upload Complete*\n\n", landlord.ApartmentName)
	fmt.Fprintf(sb, "Loaded: %d units\n", inserted)
	if skipped > 0 {
		fmt.Fprintf(sb, "Skipped (already exist): %d\n", skipped)
	}
	if len(result.Errors) > 0 {
		fmt.Fprintf(sb, "\n")
		fmt.Fprint(sb, result.FormatErrors())
	} else {
		fmt.Fprintf(sb, "\nAll tenants have been sent their payment instructions via SMS.\n")
		fmt.Fprintf(sb, "\nReply *LIST* to see your units.")
	}

	s.send(ctx, from, sb.String())
}

// sendTemplate sends the CSV column format instructions to the landlord.
func (s *Service) sendTemplate(ctx context.Context, to, paybill, apartmentName string) {
	header := "*RentLoop — CSV Template*"
	if apartmentName != "" {
		header = fmt.Sprintf("*%s — CSV Template*", apartmentName)
	}
	msg := fmt.Sprintf(
		"%s\n\n"+
			"Create a spreadsheet with these 4 columns and send it back as a CSV file:\n\n"+
			"*unit* | *name* | *phone* | *rent*\n\n"+
			"*Example rows:*\n"+
			"4B, John Kamau, 0712345678, 12500\n"+
			"2A, Mary Wanjiku, 0723456789, 8000\n"+
			"3D, Peter Otieno, 0734567890, 9500\n\n"+
			"Tips:\n"+
			"• Phone: 07XXXXXXXX or +2547XXXXXXXX\n"+
			"• Rent: numbers only e.g. 12500\n"+
			"• Unit: any reference e.g. A1, 4B, GF01\n\n"+
			"Tenants pay to Paybill *%s* using their unit as account ref.\n"+
			"They will receive an SMS with instructions once uploaded.",
		header, paybill,
	)
	s.send(ctx, to, msg)
}

func (s *Service) send(ctx context.Context, to, message string) {
	if err := s.sender.Send(ctx, to, message); err != nil {
		slog.Error("onboarding: send failed", "to", to, "error", err)
	}
}
