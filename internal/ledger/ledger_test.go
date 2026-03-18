// Package ledger_test contains black-box unit tests for the ledger service.
//
// All tests use a mock repository — no database connection is required.
// Tests cover every payment status path and the idempotency contract.
package ledger_test

import (
	"context"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/ledger"
	"github.com/codercollo/rentloop/internal/models"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	units        map[string]*models.Unit
	monthlyTotal map[string]int // key: "unitID:monthKey"
	duplicateID  string
}

func (m *mockRepo) InsertPayment(_ context.Context, p models.Payment) (*models.Payment, error) {
	if m.duplicateID != "" && p.TransactionID == m.duplicateID {
		return nil, models.ErrDuplicate
	}
	p.ID = "pay-" + p.TransactionID
	return &p, nil
}

func (m *mockRepo) GetMonthlyTotal(_ context.Context, unitID, monthKey string) (int, error) {
	return m.monthlyTotal[unitID+":"+monthKey], nil
}

func (m *mockRepo) GetUnit(_ context.Context, unitID string) (*models.Unit, error) {
	u, ok := m.units[unitID]
	if !ok {
		return nil, models.ErrNotFound
	}
	return u, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newRepo(expectedRent int) *mockRepo {
	return &mockRepo{
		units: map[string]*models.Unit{
			"u-1": {
				ID:           "u-1",
				UnitRef:      "4B",
				TenantName:   "John Kamau",
				ExpectedRent: expectedRent,
			},
		},
		monthlyTotal: make(map[string]int),
	}
}

func newPayment(amount int) models.Payment {
	return models.Payment{
		TransactionID: "TXN-" + time.Now().Format("150405.000000"),
		UnitID:        "u-1",
		LandlordID:    "ll-1",
		TenantPhone:   "254712345678",
		Amount:        amount,
		MonthKey:      "2024-03",
		PaidAt:        time.Now(),
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestRecord_FullPayment_StatusPaid(t *testing.T) {
	svc := ledger.NewService(newRepo(12500))

	recorded, err := svc.Record(context.Background(), newPayment(12500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded == nil {
		t.Fatal("expected recorded payment, got nil")
	}
	if recorded.Status != models.PaymentStatusPaid {
		t.Errorf("got status %q want paid", recorded.Status)
	}
}

func TestRecord_PartialPayment_StatusPartial(t *testing.T) {
	svc := ledger.NewService(newRepo(12500))

	recorded, err := svc.Record(context.Background(), newPayment(6000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded.Status != models.PaymentStatusPartial {
		t.Errorf("got status %q want partial", recorded.Status)
	}
}

func TestRecord_SecondPaymentClosesBalance_StatusPaid(t *testing.T) {
	repo := newRepo(12500)
	repo.monthlyTotal["u-1:2024-03"] = 6000 // first 6000 already paid

	svc := ledger.NewService(repo)

	recorded, err := svc.Record(context.Background(), newPayment(6500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded.Status != models.PaymentStatusPaid {
		t.Errorf("got status %q want paid", recorded.Status)
	}
}

func TestRecord_Overpayment_StatusOverpaid(t *testing.T) {
	svc := ledger.NewService(newRepo(12500))

	recorded, err := svc.Record(context.Background(), newPayment(25001))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded.Status != models.PaymentStatusOver {
		t.Errorf("got status %q want overpaid", recorded.Status)
	}
}

func TestRecord_ExactRentAmount_StatusPaid(t *testing.T) {
	svc := ledger.NewService(newRepo(8000))

	recorded, err := svc.Record(context.Background(), newPayment(8000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded.Status != models.PaymentStatusPaid {
		t.Errorf("got status %q want paid", recorded.Status)
	}
}

func TestRecord_DuplicateTransactionID_ReturnsNilNoError(t *testing.T) {
	repo := newRepo(12500)
	repo.duplicateID = "TXN-DUPLICATE"
	svc := ledger.NewService(repo)

	p := newPayment(12500)
	p.TransactionID = "TXN-DUPLICATE"

	recorded, err := svc.Record(context.Background(), p)
	if err != nil {
		t.Fatalf("expected no error for duplicate, got: %v", err)
	}
	if recorded != nil {
		t.Error("expected nil payment for duplicate transaction")
	}
}

func TestRecord_UnmatchedPayment_StatusUnmatched(t *testing.T) {
	svc := ledger.NewService(newRepo(12500))

	p := newPayment(12500)
	p.UnitID = "" // no unit matched

	recorded, err := svc.Record(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded.Status != "unmatched" {
		t.Errorf("got status %q want unmatched", recorded.Status)
	}
}

func TestRecord_UnknownUnit_ReturnsError(t *testing.T) {
	svc := ledger.NewService(newRepo(12500))

	p := newPayment(12500)
	p.UnitID = "non-existent"

	_, err := svc.Record(context.Background(), p)
	if err == nil {
		t.Error("expected error for unknown unit, got nil")
	}
}
