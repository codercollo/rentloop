package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/billing"
	"github.com/codercollo/rentloop/internal/models"
)

// ── Mock ─────────────────────────────────────────────────────────────────────

type mockRepo struct {
	landlord  *models.Landlord
	expired   []models.Landlord
	grace     []models.Landlord
	setStatus models.SubscriptionStatus
	activated bool
	recorded  *models.SubscriptionPayment
}

func (m *mockRepo) GetLandlordByID(_ context.Context, _ string) (*models.Landlord, error) {
	if m.landlord == nil {
		return nil, models.ErrNotFound
	}
	return m.landlord, nil
}
func (m *mockRepo) GetExpiredPaidAccounts(_ context.Context) ([]models.Landlord, error) {
	return m.expired, nil
}
func (m *mockRepo) GetGraceAccounts(_ context.Context, _ int) ([]models.Landlord, error) {
	return m.grace, nil
}
func (m *mockRepo) SetStatus(_ context.Context, _ string, s models.SubscriptionStatus) error {
	m.setStatus = s
	return nil
}
func (m *mockRepo) Activate(_ context.Context, _ string) error {
	m.activated = true
	return nil
}
func (m *mockRepo) RecordPayment(_ context.Context, p models.SubscriptionPayment) error {
	m.recorded = &p
	return nil
}
func (m *mockRepo) GetLandlordByRef(_ context.Context, _ string) (*models.Landlord, error) {
	if m.landlord == nil {
		return nil, models.ErrNotFound
	}
	return m.landlord, nil
}

type mockNotifier struct{ sent int }

func (m *mockNotifier) Send(_ context.Context, _, _ string) error {
	m.sent++
	return nil
}

// ── STK methods required by BillingRepository ──────────────────────────────

func (m *mockRepo) InsertSTKPush(_ context.Context, _, _, _ string, _ int) error {
	return nil
}

func (m *mockRepo) GetSTKRefByReceipt(_ context.Context, _ string) (string, error) {
	// For tests, we don't care about STK lookup path.
	return "RENTLOOP-ll-00000", nil
}

func (m *mockRepo) MarkSTKSuccess(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockRepo) SubscriptionPaymentExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockRepo) GetSTKRefByCheckoutID(_ context.Context, _ string) (string, error) {
	return "RENTLOOP-ll-00000", nil
}

func (m *mockRepo) MarkSTKSuccessByCheckoutID(_ context.Context, _, _ string) error {
	return nil
}

func activeLandlord(units int) *models.Landlord {
	return &models.Landlord{
		ID:                 "ll-00000001-0000-0000-0000-000000000001",
		WhatsAppPhone:      "+254781423339",
		Name:               "Test Landlord",
		PaybillNumber:      "174379",
		SubscriptionStatus: models.StatusActive,
		UnitCount:          units,
		BillingCycleEnd:    func() *time.Time { t := time.Now().AddDate(0, -1, 0); return &t }(),
	}
}

func newSvc(repo *mockRepo) *billing.Service {
	return billing.NewService(repo, &mockNotifier{}, 5, 50, 10)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestMonthlyAmount_FreeTier_ReturnsZero(t *testing.T) {
	svc := newSvc(&mockRepo{})
	if got := svc.MonthlyAmount(8); got != 0 {
		t.Errorf("expected 0 for free tier, got %d", got)
	}
	if got := svc.MonthlyAmount(10); got != 0 {
		t.Errorf("expected 0 at limit, got %d", got)
	}
}

func TestMonthlyAmount_PaidTier_CalculatesCorrectly(t *testing.T) {
	svc := newSvc(&mockRepo{})
	if got := svc.MonthlyAmount(20); got != 1000 {
		t.Errorf("expected 1000 for 20 units, got %d", got)
	}
}

func TestTransitionExpired_MovesToGrace(t *testing.T) {
	repo := &mockRepo{expired: []models.Landlord{*activeLandlord(20)}}
	svc := newSvc(repo)

	if err := svc.TransitionExpired(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.setStatus != models.StatusGrace {
		t.Errorf("expected grace status, got %v", repo.setStatus)
	}
}

func TestTransitionExpired_FreeTier_NeverSuspended(t *testing.T) {
	repo := &mockRepo{expired: []models.Landlord{*activeLandlord(8)}}
	svc := newSvc(repo)

	if err := svc.TransitionExpired(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.setStatus == models.StatusSuspended {
		t.Error("free tier should never be suspended")
	}
}

func TestTransitionGrace_MovesToSuspended(t *testing.T) {
	repo := &mockRepo{grace: []models.Landlord{*activeLandlord(20)}}
	svc := newSvc(repo)

	if err := svc.TransitionGrace(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.setStatus != models.StatusSuspended {
		t.Errorf("expected suspended, got %v", repo.setStatus)
	}
}

func TestProcessPayment_FullAmount_Activates(t *testing.T) {
	repo := &mockRepo{landlord: activeLandlord(20)}
	svc := newSvc(repo)

	err := svc.ProcessPayment(context.Background(), "TXN-001", "RENTLOOP-ll-00000", 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.activated {
		t.Error("expected account to be activated")
	}
}

func TestProcessPayment_Underpayment_DoesNotActivate(t *testing.T) {
	repo := &mockRepo{landlord: activeLandlord(20)}
	svc := newSvc(repo)

	err := svc.ProcessPayment(context.Background(), "TXN-002", "RENTLOOP-ll-00000", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.activated {
		t.Error("should not activate on underpayment")
	}
}

func TestIsSubscriptionPayment(t *testing.T) {
	if !billing.IsSubscriptionPayment("RENTLOOP-abc12345") {
		t.Error("expected true for RENTLOOP- prefix")
	}
	if billing.IsSubscriptionPayment("B01") {
		t.Error("expected false for regular unit ref")
	}
}
