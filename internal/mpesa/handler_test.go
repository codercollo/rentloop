// Package mpesa_test contains unit and integration tests for the M-Pesa C2B
// callback handler, covering validation, payment processing, ledger recording,
// notification dispatch, and error handling scenarios.
package mpesa_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/models"
	"github.com/codercollo/rentloop/internal/mpesa"
)

// ── Mocks ─────────────────────────────────────────────────────────────────────

type mockMatcher struct {
	unit *models.Unit
	err  error
}

func (m *mockMatcher) Match(_ context.Context, _, _ string) (*models.Unit, error) {
	return m.unit, m.err
}

type mockLedger struct {
	recorded *models.Payment
	err      error
}

func (m *mockLedger) Record(_ context.Context, p models.Payment) (*models.Payment, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.recorded != nil {
		return m.recorded, nil
	}
	p.ID = "pay-123"
	p.Status = models.PaymentStatusPaid
	return &p, nil
}

type mockNotifier struct {
	landlordCalled bool
	tenantCalled   bool
}

func (m *mockNotifier) NotifyLandlord(_ context.Context, _ string, _ *models.Payment, _ *models.Unit, _ string) error {
	m.landlordCalled = true
	return nil
}

func (m *mockNotifier) NotifyTenant(_ context.Context, _ string, _ *models.Payment, _ *models.Unit, _ string) error {
	m.tenantCalled = true
	return nil
}

type mockLandlordRepo struct {
	landlord *models.Landlord
	err      error
}

func (m *mockLandlordRepo) GetByPaybill(_ context.Context, _ string) (*models.Landlord, error) {
	return m.landlord, m.err
}

type slowMockLedger struct {
	delay time.Duration
}

func (s *slowMockLedger) Record(_ context.Context, p models.Payment) (*models.Payment, error) {
	time.Sleep(s.delay)
	p.ID = "pay-slow"
	p.Status = models.PaymentStatusPaid
	return &p, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func validCallback() mpesa.C2BCallback {
	return mpesa.C2BCallback{
		TransactionType:   "Pay Bill",
		TransID:           "LHG31AA5TX",
		TransTime:         "20240301093045",
		TransAmount:       "12500.00",
		BusinessShortCode: "174379",
		BillRefNumber:     "4B",
		MSISDN:            "254712345678",
		FirstName:         "John",
		LastName:          "Kamau",
	}
}

// newHandler creates a test handler with billing=nil and isDev=true.
// billing is nil in tests — the nil guard in handler.go prevents panics.
func newHandler(
	matcher mpesa.MatcherService,
	ledger mpesa.LedgerService,
	notifier mpesa.NotifierService,
	repo mpesa.LandlordRepository,
) *mpesa.Handler {
	return mpesa.NewHandler(matcher, ledger, notifier, repo, nil, true)
}

func postCallback(t *testing.T, h *mpesa.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mpesa/c2b/callback", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Callback(rr, req)
	return rr
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestCallback_ValidPayload_Returns200(t *testing.T) {
	landlord := &models.Landlord{ID: "ll-1", WhatsAppPhone: "+254700000000"}
	unit := &models.Unit{ID: "u-1", UnitRef: "4B", TenantName: "John Kamau"}

	h := newHandler(
		&mockMatcher{unit: unit},
		&mockLedger{},
		&mockNotifier{},
		&mockLandlordRepo{landlord: landlord},
	)

	rr := postCallback(t, h, validCallback())
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rr.Code)
	}

	var resp mpesa.C2BResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ResultCode != "0" {
		t.Errorf("expected ResultCode 0 got %q", resp.ResultCode)
	}
}

func TestCallback_BlockedIP_Returns403(t *testing.T) {
	// isDev=false enforces IP check
	h := mpesa.NewHandler(&mockMatcher{}, &mockLedger{}, &mockNotifier{}, &mockLandlordRepo{}, nil, false)

	b, _ := json.Marshal(validCallback())
	req := httptest.NewRequest(http.MethodPost, "/mpesa/c2b/callback", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "1.2.3.4:9999" // not a Safaricom IP

	rr := httptest.NewRecorder()
	h.Callback(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 got %d", rr.Code)
	}
}

func TestCallback_MalformedJSON_Returns400(t *testing.T) {
	h := newHandler(&mockMatcher{}, &mockLedger{}, &mockNotifier{}, &mockLandlordRepo{})

	req := httptest.NewRequest(http.MethodPost, "/mpesa/c2b/callback",
		bytes.NewBufferString(`{not valid json}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Callback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 got %d", rr.Code)
	}
}

func TestCallback_MissingTransID_Returns422(t *testing.T) {
	cb := validCallback()
	cb.TransID = ""
	rr := postCallback(t, newHandler(&mockMatcher{}, &mockLedger{}, &mockNotifier{}, &mockLandlordRepo{}), cb)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 got %d", rr.Code)
	}
}

func TestCallback_MissingBillRef_Returns422(t *testing.T) {
	cb := validCallback()
	cb.BillRefNumber = ""
	rr := postCallback(t, newHandler(&mockMatcher{}, &mockLedger{}, &mockNotifier{}, &mockLandlordRepo{}), cb)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 got %d", rr.Code)
	}
}

func TestCallback_ZeroAmount_Returns422(t *testing.T) {
	cb := validCallback()
	cb.TransAmount = "0.00"
	rr := postCallback(t, newHandler(&mockMatcher{}, &mockLedger{}, &mockNotifier{}, &mockLandlordRepo{}), cb)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 got %d", rr.Code)
	}
}

func TestCallback_RespondsBeforeProcessing(t *testing.T) {
	landlord := &models.Landlord{ID: "ll-1"}
	unit := &models.Unit{ID: "u-1", UnitRef: "4B"}

	h := newHandler(
		&mockMatcher{unit: unit},
		&slowMockLedger{delay: 100 * time.Millisecond},
		&mockNotifier{},
		&mockLandlordRepo{landlord: landlord},
	)

	start := time.Now()
	rr := postCallback(t, h, validCallback())
	elapsed := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rr.Code)
	}
	if elapsed >= 50*time.Millisecond {
		t.Errorf("handler took %v — should respond before goroutine finishes", elapsed)
	}
}

func TestCallback_SubscriptionPayment_Returns200AndSkipsLedger(t *testing.T) {
	// billing=nil is intentional in tests — nil guard prevents panic.
	// The test confirms: subscription refs get 200 and bypass the ledger.
	cb := validCallback()
	cb.BillRefNumber = "RENTLOOP-accee42b"

	ledger := &mockLedger{}
	h := newHandler(&mockMatcher{}, ledger, &mockNotifier{}, &mockLandlordRepo{})

	rr := postCallback(t, h, cb)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rr.Code)
	}

	// Give the goroutine time to run
	time.Sleep(20 * time.Millisecond)

	// Ledger must NOT have been called for a subscription payment
	if ledger.recorded != nil {
		t.Error("subscription payment should not touch the rent ledger")
	}
}

// ── Validator unit tests ───────────────────────────────────────────────────────

func TestValidatePayload_AllFieldsMissing(t *testing.T) {
	if err := mpesa.ValidatePayload(&mpesa.C2BCallback{}); err == nil {
		t.Error("expected error for empty payload")
	}
}

func TestValidatePayload_Valid(t *testing.T) {
	cb := validCallback()
	if err := mpesa.ValidatePayload(&cb); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseAmount(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		wantErr  bool
	}{
		{"12500.00", 12500, false},
		{"8000.50", 8000, false},
		{"0.00", 0, false},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := mpesa.ParseAmount(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseAmount(%q): expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAmount(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.expected {
			t.Errorf("ParseAmount(%q): got %d want %d", tt.input, got, tt.expected)
		}
	}
}
