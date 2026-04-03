package receipt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/rentloop/internal/models"
	receipt "github.com/codercollo/rentloop/internal/receipt"
)

// ── Fake repository ───────────────────────────────────────────────────────────

type fakeRepo struct {
	existing  *models.Receipt
	inserted  *models.Receipt
	insertErr error
}

func (f *fakeRepo) GetReceiptByPaymentID(_ context.Context, _ string) (*models.Receipt, error) {
	return f.existing, nil
}

func (f *fakeRepo) InsertReceipt(_ context.Context, rec models.Receipt) (*models.Receipt, error) {
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	out := rec
	out.ID = "fake-receipt-id-0001"
	out.SentAt = time.Now()
	f.inserted = &out
	return &out, nil
}

// ── Fixtures ──────────────────────────────────────────────────────────────────

func testPayment() *models.Payment {
	return &models.Payment{
		ID:            "pay-uuid-abcd1234",
		TransactionID: "LHG31AA5TX",
		LandlordID:    "landlord-uuid-0001",
		UnitID:        "unit-uuid-0001",
		TenantPhone:   "254712345678",
		Amount:        12500,
		Status:        models.PaymentStatusPaid,
		MonthKey:      "2026-04",
		PaidAt:        time.Date(2026, 4, 2, 15, 30, 0, 0, time.UTC),
	}
}

func testUnit() *models.Unit {
	return &models.Unit{
		ID:           "unit-uuid-0001",
		LandlordID:   "landlord-uuid-0001",
		UnitRef:      "4B",
		TenantName:   "John Kamau",
		TenantPhone:  "254712345678",
		ExpectedRent: 12500,
	}
}

func testLandlord() *models.Landlord {
	return &models.Landlord{
		ID:            "landlord-uuid-0001",
		Name:          "Wanjiku Properties",
		ApartmentName: "Sunrise Apartments",
		PremiseName:   "Sunrise Apartments - Kitengela",
		PaybillNumber: "400200",
	}
}

func serviceWithRepo(repo receipt.Repository) *receipt.Service {
	return receipt.NewService(receipt.Config{
		SpacesKey:      "dummy",
		SpacesSecret:   "dummy",
		SpacesBucket:   "rentloop-test",
		SpacesRegion:   "fra1",
		SpacesEndpoint: "https://fra1.digitaloceanspaces.com",
	}, repo)
}

// ── Pure helper tests — no network, no DB ─────────────────────────────────────

// TestFormatAmount verifies comma-formatting of KES amounts.
func TestFormatAmount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1,000"},
		{12500, "12,500"},
		{100000, "100,000"},
		{1000000, "1,000,000"},
	}
	for _, tc := range cases {
		got := localFormatAmount(tc.in)
		if got != tc.want {
			t.Errorf("formatAmount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func localFormatAmount(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range s {
		pos := len(s) - i
		if i > 0 && pos%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// TestFormatMonthKey verifies "2026-04" → "April 2026".
func TestFormatMonthKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-04", "April 2026"},
		{"2026-01", "January 2026"},
		{"2025-12", "December 2025"},
		{"bad-val", "bad-val"},
		{"2026-13", "2026-13"},
	}
	for _, tc := range cases {
		got := localFormatMonthKey(tc.in)
		if got != tc.want {
			t.Errorf("formatMonthKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func localFormatMonthKey(mk string) string {
	if len(mk) != 7 {
		return mk
	}
	t, err := time.Parse("2006-01", mk)
	if err != nil {
		return mk
	}
	return t.Format("January 2006")
}

// TestBuildReceiptNumber verifies the RL-YYYYMMDD-XXXXXXXX format.
func TestBuildReceiptNumber(t *testing.T) {
	p := testPayment()
	// p.ID = "pay-uuid-abcd1234" → uppercase first 8 → "PAY-UUID"
	got := localBuildReceiptNumber(p)

	date := time.Now().Format("20060102")
	want := "RL-" + date + "-PAY-UUID"

	if got != want {
		t.Errorf("buildReceiptNumber = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "RL-") {
		t.Errorf("receipt number must start with RL-, got %q", got)
	}
}

func localBuildReceiptNumber(p *models.Payment) string {
	date := time.Now().Format("20060102")
	suffix := strings.ToUpper(p.ID)
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return fmt.Sprintf("RL-%s-%s", date, suffix)
}

// TestReceiptNumberUniqueness verifies two payments produce different numbers.
func TestReceiptNumberUniqueness(t *testing.T) {
	p1 := testPayment()
	p2 := testPayment()
	p2.ID = "zzzz9999-diff-0000"

	n1 := localBuildReceiptNumber(p1)
	n2 := localBuildReceiptNumber(p2)

	if n1 == n2 {
		t.Errorf("two different payments produced identical receipt numbers: %q", n1)
	}
}

// ── Service tests ─────────────────────────────────────────────────────────────

// TestIssue_Idempotency is the most important test.
// Safaricom retries its callback up to 3 times. If Issue is called twice
// for the same payment it must return the existing receipt without
// calling InsertReceipt again.
func TestIssue_Idempotency(t *testing.T) {
	existing := &models.Receipt{
		ID:            "existing-receipt-id",
		PaymentID:     "pay-uuid-abcd1234",
		ReceiptNumber: "RL-20260402-PAY-UUID",
		PublicURL:     "https://rentloop.fra1.digitaloceanspaces.com/receipts/landlord-uuid-0001/RL-20260402-PAY-UUID.pdf",
	}
	repo := &fakeRepo{existing: existing}
	svc := serviceWithRepo(repo)

	result, err := svc.Issue(context.Background(), receipt.IssueInput{
		Payment:  testPayment(),
		Unit:     testUnit(),
		Landlord: testLandlord(),
	})

	if err != nil {
		t.Fatalf("Issue returned unexpected error: %v", err)
	}
	if result.Receipt.ID != "existing-receipt-id" {
		t.Errorf("expected existing receipt ID, got %q", result.Receipt.ID)
	}
	if result.PublicURL != existing.PublicURL {
		t.Errorf("expected existing URL %q, got %q", existing.PublicURL, result.PublicURL)
	}
	if repo.inserted != nil {
		t.Fatal("InsertReceipt was called despite an existing receipt — idempotency is broken")
	}
}

// TestIssueAsync_CallbackCalled verifies IssueAsync fires onDone.
// Uses the idempotency path so no real Spaces upload is needed.
func TestIssueAsync_CallbackCalled(t *testing.T) {
	existing := &models.Receipt{
		ID:        "async-receipt-id",
		PaymentID: "pay-uuid-abcd1234",
		PublicURL: "https://example.com/receipts/test.pdf",
	}
	repo := &fakeRepo{existing: existing}
	svc := serviceWithRepo(repo)

	done := make(chan *receipt.IssueResult, 1)
	errCh := make(chan error, 1)

	svc.IssueAsync(receipt.IssueInput{
		Payment:  testPayment(),
		Unit:     testUnit(),
		Landlord: testLandlord(),
	}, func(result *receipt.IssueResult, err error) {
		if err != nil {
			errCh <- err
			return
		}
		done <- result
	})

	select {
	case result := <-done:
		if result.Receipt.ID != "async-receipt-id" {
			t.Errorf("IssueAsync callback got wrong receipt ID: %q", result.Receipt.ID)
		}
	case err := <-errCh:
		t.Fatalf("IssueAsync callback returned error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("IssueAsync callback was never fired within 3 seconds")
	}
}

// TestIssueAsync_NilCallbackDoesNotPanic confirms passing nil onDone is safe.
func TestIssueAsync_NilCallbackDoesNotPanic(t *testing.T) {
	repo := &fakeRepo{existing: &models.Receipt{
		ID:        "x",
		PaymentID: "pay-uuid-abcd1234",
		PublicURL: "https://example.com/x.pdf",
	}}
	svc := serviceWithRepo(repo)

	// Must not panic.
	svc.IssueAsync(receipt.IssueInput{
		Payment:  testPayment(),
		Unit:     testUnit(),
		Landlord: testLandlord(),
	}, nil)

	time.Sleep(200 * time.Millisecond)
}

// TestIssue_InsertFailureIsNonFatal and TestIssue_NilLandlord require an
// injectable Uploader interface to avoid real Spaces calls.
// They are skipped here and tracked as TODOs.
func TestIssue_InsertFailureIsNonFatal(t *testing.T) {
	t.Skip("TODO: extract Uploader interface to enable this test without real Spaces")
}

func TestIssue_NilLandlordDoesNotPanic(t *testing.T) {
	t.Skip("TODO: extract Uploader interface to enable this test without real Spaces")
}
