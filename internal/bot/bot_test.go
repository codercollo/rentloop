// // Package bot_test contains black-box unit tests for the bot service.
// // All tests use mocks — no database or HTTP calls required.
package bot_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/bot"
	"github.com/codercollo/rentloop/internal/models"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	landlord *models.Landlord
	agent    *models.Agent
	units    []bot.UnitStatus
}

func (m *mockRepo) GetLandlordByPhone(_ context.Context, _ string) (*models.Landlord, error) {
	if m.landlord == nil {
		return nil, models.ErrNotFound
	}
	return m.landlord, nil
}

func (m *mockRepo) GetAgentByPhone(_ context.Context, _ string) (*models.Agent, error) {
	if m.agent == nil {
		return nil, models.ErrNotFound
	}
	return m.agent, nil
}

func (m *mockRepo) GetUnitsWithStatus(_ context.Context, _, _ string) ([]bot.UnitStatus, error) {
	return m.units, nil
}

func (m *mockRepo) GetLandlordsByAgent(_ context.Context, _ string) ([]models.Landlord, error) {
	if m.landlord != nil {
		return []models.Landlord{*m.landlord}, nil
	}
	return nil, nil
}

func (m *mockRepo) GetLandlordByAgentAndName(_ context.Context, _, _ string) (*models.Landlord, error) {
	if m.landlord == nil {
		return nil, models.ErrNotFound
	}
	return m.landlord, nil
}

func (m *mockRepo) GetPaymentHistory(_ context.Context, _ string, _ int) ([]bot.PaymentHistoryRow, error) {
	return []bot.PaymentHistoryRow{
		{MonthKey: time.Now().Format("2006-01"), Amount: 12500, Status: "paid"},
	}, nil
}

func (m *mockRepo) GetUnitByRef(_ context.Context, _, _ string) (*models.Unit, error) {
	if len(m.units) == 0 {
		return nil, models.ErrNotFound
	}
	u := m.units[0].Unit
	return &u, nil
}

func (m *mockRepo) GetUnmatchedPayment(_ context.Context, _, _ string) (*models.Payment, error) {
	return &models.Payment{
		ID: "pay-1", TransactionID: "TXN-ABC123",
		Amount: 12500, LandlordID: "ll-1",
	}, nil
}

func (m *mockRepo) AssignPaymentToUnit(
	_ context.Context,
	_ string,
	_ string,
	_ int,
	_ int,
) error {
	return nil
}

func (m *mockRepo) InsertUnit(_ context.Context, u models.Unit) (*models.Unit, error) {
	u.ID = "u-new"
	return &u, nil
}

func (m *mockRepo) GetLandlordsWithUnpaid(_ context.Context, _ string) ([]models.Landlord, error) {
	if m.landlord != nil {
		return []models.Landlord{*m.landlord}, nil
	}
	return nil, nil
}

func (m *mockRepo) UpdateExpectedRent(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (m *mockRepo) UpdateUnitCount(ctx context.Context, landlordID string, count int) error {
	// For testing, just store the count if you want, or do nothing
	return nil
}

func (m *mockRepo) ReplaceUnitTenant(_ context.Context, _ string, u models.Unit) (*models.Unit, error) {
	u.ID = "u-replaced"
	return &u, nil
}

func (m *mockRepo) InsertManualPayment(_ context.Context, p models.Payment) (*models.Payment, error) {
	p.ID = "pay-manual"
	return &p, nil
}

func (m *mockRepo) GetPaymentHistory12(_ context.Context, _ string) ([]models.MonthlyPaymentRow, error) {
	return nil, nil
}

func (m *mockRepo) GetPortfolioHistory12(_ context.Context, _ string) ([]models.PortfolioMonthRow, error) {
	return nil, nil
}

// ── Mock sender ───────────────────────────────────────────────────────────────

type mockSender struct {
	lastTo  string
	lastMsg string
}

func (m *mockSender) Send(_ context.Context, to, msg string) error {
	m.lastTo = to
	m.lastMsg = msg
	return nil
}

type mockSMS struct {
	reminders  int
	onboarding int
}

func (m *mockSMS) SendReminder(_ context.Context, _, _, _ string, _, _ int, _ string) error {
	m.reminders++
	return nil
}

func (m *mockSMS) SendOnboarding(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ string,
	_ string,
	_ int,
) error {
	m.onboarding++
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func activeLandlord() *models.Landlord {
	return &models.Landlord{
		ID: "ll-1", WhatsAppPhone: "+254712345678",
		Name: "James Mwangi", PaybillNumber: "174379",
		SubscriptionStatus: "active", UnitCount: 2,
	}
}

func unitsWithMixed() []bot.UnitStatus {
	return []bot.UnitStatus{
		{
			Unit:      models.Unit{ID: "u-1", UnitRef: "4B", TenantName: "John Kamau", TenantPhone: "254712000001", ExpectedRent: 12500},
			TotalPaid: 12500, IsPaid: true,
		},
		{
			Unit:      models.Unit{ID: "u-2", UnitRef: "2A", TenantName: "Mary Wanjiku", TenantPhone: "254712000002", ExpectedRent: 8000},
			TotalPaid: 0, IsPaid: false,
		},
	}
}

func newService(repo *mockRepo, sender *mockSender, sms *mockSMS) *bot.Service {
	return bot.NewService(repo, sender, sms)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestHandle_UnknownSender_SendsWelcome(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254799999999", "LIST")

	if !strings.Contains(sender.lastMsg, "Welcome") {
		t.Errorf("expected welcome message, got: %s", sender.lastMsg)
	}
}

func TestHandle_SuspendedLandlord_SendsLockout(t *testing.T) {
	l := activeLandlord()
	l.SubscriptionStatus = "suspended"

	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: l}, sender, &mockSMS{})

	svc.Handle(context.Background(), l.WhatsAppPhone, "LIST")

	if !strings.Contains(sender.lastMsg, "Suspended") {
		t.Errorf("expected suspended message, got: %s", sender.lastMsg)
	}
}

func TestHandle_GraceLandlord_AppendsBillingWarning(t *testing.T) {
	l := activeLandlord()
	l.SubscriptionStatus = "grace"

	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: l, units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), l.WhatsAppPhone, "LIST")

	if !strings.Contains(sender.lastMsg, "Subscription due") {
		t.Errorf("expected grace warning, got: %s", sender.lastMsg)
	}
}

func TestHandle_LIST_ShowsPaidAndUnpaid(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "LIST")

	if !strings.Contains(sender.lastMsg, "4B") {
		t.Errorf("expected unit 4B in LIST output, got: %s", sender.lastMsg)
	}
	if !strings.Contains(sender.lastMsg, "2A") {
		t.Errorf("expected unit 2A in LIST output")
	}
}

func TestHandle_REMIND_SendsSMSToUnpaid(t *testing.T) {
	sms := &mockSMS{}
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, sms)

	svc.Handle(context.Background(), "+254712345678", "REMIND")

	if sms.reminders != 1 {
		t.Errorf("expected 1 reminder (1 unpaid unit), got %d", sms.reminders)
	}
}

func TestHandle_TOTAL_ShowsCollectedVsExpected(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "TOTAL")

	if !strings.Contains(sender.lastMsg, "Collected") {
		t.Errorf("expected TOTAL output with Collected, got: %s", sender.lastMsg)
	}
}

func TestHandle_RECEIPT_ValidUnit_ReturnsReceipt(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "RECEIPT 4B")

	if !strings.Contains(sender.lastMsg, "Receipt") {
		t.Errorf("expected receipt output, got: %s", sender.lastMsg)
	}
}

func TestHandle_CLAIM_AssignsPayment(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "CLAIM TXN-ABC123 TO 4B")

	if !strings.Contains(sender.lastMsg, "assigned") {
		t.Errorf("expected assigned confirmation, got: %s", sender.lastMsg)
	}
}

func TestHandle_UnknownCommand_ReturnsHelp(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "XYZUNKNOWN")

	if !strings.Contains(sender.lastMsg, "Commands") {
		t.Errorf("expected help text for unknown command, got: %s", sender.lastMsg)
	}
}

func TestHandle_SET_RENT_UpdatesRent(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "SET RENT 4B 14000")

	if !strings.Contains(sender.lastMsg, "updated") {
		t.Errorf("expected rent updated confirmation, got: %s", sender.lastMsg)
	}
}

func TestHandle_REPLACE_ReplacesTenant(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "REPLACE 4B Grace Auma 0745678901 12500")

	if !strings.Contains(sender.lastMsg, "replaced") {
		t.Errorf("expected replacement confirmation, got: %s", sender.lastMsg)
	}
}

func TestHandle_MARK_RecordsManualPayment(t *testing.T) {
	sender := &mockSender{}
	svc := newService(&mockRepo{landlord: activeLandlord(), units: unitsWithMixed()}, sender, &mockSMS{})

	svc.Handle(context.Background(), "+254712345678", "MARK 4B PAID 12500 BANK")

	if !strings.Contains(sender.lastMsg, "recorded") {
		t.Errorf("expected payment recorded confirmation, got: %s", sender.lastMsg)
	}
}
