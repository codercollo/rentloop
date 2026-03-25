// Package notifier_test contains black-box tests for the WhatsApp notifier.
// Tests use a local httptest.Server — no Africa's Talking account required.
package notifier_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/models"
	"github.com/codercollo/rentloop/internal/notifier"
)

// testWA creates a WhatsApp notifier pointed at a fake HTTP server.
func testWA(t *testing.T, handler http.HandlerFunc) (*notifier.WhatsApp, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	wa := notifier.NewWhatsAppWithURL(srv.URL, "test-key", "sandbox", "+254700000000")
	return wa, srv
}

func TestNotifyLandlord_FullPayment_MessageContainsNameAndAmount(t *testing.T) {
	var gotBody string
	wa, srv := testWA(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotBody = r.FormValue("message")
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	p := &models.Payment{
		ID: "pay-abc12345", Amount: 12500,
		Status: models.PaymentStatusPaid,
		PaidAt: time.Now(),
	}
	unit := &models.Unit{UnitRef: "4B", TenantName: "John Kamau"}

	if err := wa.NotifyLandlord(context.Background(), "+254712345678", p, unit, "Sunrise Apartments"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, "John Kamau") {
		t.Errorf("expected tenant name in message, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "12500") {
		t.Errorf("expected amount in message, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "4B") {
		t.Errorf("expected unit ref in message, got: %s", gotBody)
	}
}

func TestNotifyLandlord_PartialPayment_MessageContainsPartialText(t *testing.T) {
	var gotBody string
	wa, srv := testWA(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotBody = r.FormValue("message")
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	p := &models.Payment{
		ID: "pay-partial1", Amount: 6000,
		Status: models.PaymentStatusPartial,
		PaidAt: time.Now(),
	}
	unit := &models.Unit{UnitRef: "2A", TenantName: "Mary Wanjiku"}

	if err := wa.NotifyLandlord(context.Background(), "+254712345678", p, unit, "Sunrise Apartments"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, "Partial") {
		t.Errorf("expected Partial status in message, got: %s", gotBody)
	}
}

func TestNotifyLandlord_UnmatchedPayment_MessageContainsClaim(t *testing.T) {
	var gotBody string
	wa, srv := testWA(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotBody = r.FormValue("message")
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	p := &models.Payment{
		ID: "pay-unmatched", TransactionID: "LHG31AA5TX",
		Amount: 8000, TenantPhone: "254712000001",
	}

	if err := wa.NotifyLandlord(context.Background(), "+254712345678", p, nil, "Sunrise Apartments"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, "CLAIM") {
		t.Errorf("expected CLAIM instruction in unmatched message, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "LHG31AA5TX") {
		t.Errorf("expected transaction ID in message, got: %s", gotBody)
	}
}

func TestSendRaw_Success_Returns200(t *testing.T) {
	wa, srv := testWA(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	err := wa.SendRaw(context.Background(), "+254712345678", "test message")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendRaw_ATReturns400_ReturnsError(t *testing.T) {
	wa, srv := testWA(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	defer srv.Close()

	err := wa.SendRaw(context.Background(), "+254712345678", "test message")
	if err == nil {
		t.Error("expected error when AT returns 400, got nil")
	}
}

func TestSendRaw_CorrectHeaders(t *testing.T) {
	var gotAPIKey, gotContentType string
	wa, srv := testWA(t, func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("apiKey")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	_ = wa.SendRaw(context.Background(), "+254712345678", "hello")

	if gotAPIKey != "test-key" {
		t.Errorf("expected apiKey header 'test-key', got %q", gotAPIKey)
	}
	if !strings.Contains(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("expected form-encoded content type, got %q", gotContentType)
	}
}
