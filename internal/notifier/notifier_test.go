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

func testTwilio(t *testing.T, handler http.HandlerFunc) (*notifier.Twilio, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	tw := notifier.NewTwilioWithURL(srv.URL, "ACTEST", "token", "whatsapp:+14155238886", "+14155238886")
	return tw, srv
}

func TestTwilio_NotifyLandlord_SendsWhatsApp(t *testing.T) {
	var gotTo, gotFrom string
	tw, srv := testTwilio(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotTo = r.FormValue("To")
		gotFrom = r.FormValue("From")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	})
	defer srv.Close()

	p := &models.Payment{
		ID: "pay-abc12345", Amount: 12500,
		Status: models.PaymentStatusPaid,
		PaidAt: time.Now(),
	}
	unit := &models.Unit{UnitRef: "4B", TenantName: "John Kamau"}

	err := tw.NotifyLandlord(context.Background(), "+254781423339", p, unit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(gotTo, "whatsapp:") {
		t.Errorf("expected whatsapp: prefix on To, got: %s", gotTo)
	}
	if !strings.Contains(gotTo, "254781423339") {
		t.Errorf("expected landlord number in To, got: %s", gotTo)
	}
	if gotFrom != "whatsapp:+14155238886" {
		t.Errorf("expected sandbox from number, got: %s", gotFrom)
	}
}

func TestTwilio_NotifyTenant_SendsSMS(t *testing.T) {
	var gotBody string
	tw, srv := testTwilio(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotBody = r.FormValue("Body")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	})
	defer srv.Close()

	p := &models.Payment{
		ID: "pay-tenant01", Amount: 10000,
		Status: models.PaymentStatusPaid,
		PaidAt: time.Now(),
	}
	unit := &models.Unit{UnitRef: "B01", TenantName: "Test Tenant"}

	err := tw.NotifyTenant(context.Background(), "254741775492", p, unit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, "B01") {
		t.Errorf("expected unit ref in SMS body, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "10000") {
		t.Errorf("expected amount in SMS body, got: %s", gotBody)
	}
}

func TestTwilio_SendRaw_WhatsApp(t *testing.T) {
	var gotTo string
	tw, srv := testTwilio(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotTo = r.FormValue("To")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	})
	defer srv.Close()

	err := tw.SendRaw(context.Background(), "+254781423339", "LIST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(gotTo, "whatsapp:") {
		t.Errorf("expected whatsapp: prefix, got: %s", gotTo)
	}
}

func TestTwilio_ErrorResponse_ReturnsError(t *testing.T) {
	tw, srv := testTwilio(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized","code":20003}`))
	})
	defer srv.Close()

	err := tw.SendRaw(context.Background(), "+254781423339", "test")
	if err == nil {
		t.Error("expected error for 401 response, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestTwilio_SendReminder_FormatsCorrectly(t *testing.T) {
	var gotBody string
	tw, srv := testTwilio(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotBody = r.FormValue("Body")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"sid":"SM123"}`))
	})
	defer srv.Close()

	err := tw.SendReminder(context.Background(), "254741775492", "John Kamau", "4B", 12500, "March 2026")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, "John Kamau") {
		t.Errorf("expected tenant name in reminder, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "12500") {
		t.Errorf("expected amount in reminder, got: %s", gotBody)
	}
}
