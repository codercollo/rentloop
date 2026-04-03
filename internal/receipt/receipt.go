// Package receipt generates PDF rent receipts and uploads them to
// DigitalOcean Spaces (S3-compatible). A receipt row is then written
// to the `receipts` table and the public URL is sent to the tenant
// via WhatsApp/SMS.
//
// PDF layout (A4, portrait):
//
//	┌─────────────────────────────────────────┐
//	│  RentLoop                    [logo area] │
//	│  Tax Receipt                             │
//	├─────────────────────────────────────────┤
//	│  Receipt No: RL-20260402-ABCD1234        │
//	│  Date:       02 Apr 2026                 │
//	│  Period:     April 2026                  │
//	├─────────────────────────────────────────┤
//	│  Landlord:   Sunrise Apartments          │
//	│  Tenant:     John Kamau                  │
//	│  Unit:       4B                          │
//	├─────────────────────────────────────────┤
//	│  Amount Paid:  KES 12,500                │
//	│  Status:       Paid ✓                    │
//	└─────────────────────────────────────────┘
package receipt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jung-kurt/gofpdf"

	"github.com/codercollo/rentloop/internal/models"
)

// ── Config ────────────────────────────────────────────────────────────────────

// Config holds DigitalOcean Spaces credentials and bucket info.
// Load from environment via config.Load() — do not hard-code secrets here.
type Config struct {
	SpacesKey      string // DO_SPACES_KEY
	SpacesSecret   string // DO_SPACES_SECRET
	SpacesBucket   string // DO_SPACES_BUCKET
	SpacesRegion   string // DO_SPACES_REGION  e.g. "fra1"
	SpacesEndpoint string // DO_SPACES_ENDPOINT e.g. "https://fra1.digitaloceanspaces.com"
	CDNBase        string // optional CDN prefix — falls back to Spaces public URL
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service generates and stores rent receipts.
type Service struct {
	cfg      Config
	repo     Repository
	uploader Uploader
}

type spacesUploader struct {
	cfg Config
}

// Repository is the only database dependency the receipt service needs.
type Repository interface {
	InsertReceipt(ctx context.Context, rec models.Receipt) (*models.Receipt, error)
	GetReceiptByPaymentID(ctx context.Context, paymentID string) (*models.Receipt, error)
}

// Uploader stores a PDF and returns its public URL.
// Swap in a fakeUploader for local dev — no Spaces credentials needed.
type Uploader interface {
	Upload(ctx context.Context, key string, data []byte) (string, error)
}

// NewService wires config and repo.
func NewService(cfg Config, repo Repository) *Service {
	return &Service{cfg: cfg, repo: repo, uploader: &spacesUploader{cfg: cfg}}
}

// NewServiceWithUploader lets callers inject a custom uploader (e.g. fakeUploader in dev).
func NewServiceWithUploader(cfg Config, repo Repository, u Uploader) *Service {
	return &Service{cfg: cfg, repo: repo, uploader: u}
}

// ── Issue ─────────────────────────────────────────────────────────────────────

// IssueInput holds everything needed to generate and store a receipt.
type IssueInput struct {
	Payment       *models.Payment
	Unit          *models.Unit
	Landlord      *models.Landlord
	ApartmentName string // premise_name → apartment_name fallback
}

// IssueResult is returned to callers after a successful issue.
type IssueResult struct {
	Receipt   *models.Receipt
	PublicURL string
}

// Issue generates the PDF, uploads it, and persists the receipt row.
// It is idempotent: if a receipt already exists for the payment it returns
// the existing row without re-generating.
func (s *Service) Issue(ctx context.Context, in IssueInput) (*IssueResult, error) {
	// ── Idempotency check ─────────────────────────────────────────────────────
	existing, err := s.repo.GetReceiptByPaymentID(ctx, in.Payment.ID)
	if err == nil && existing != nil {
		return &IssueResult{Receipt: existing, PublicURL: existing.PublicURL}, nil
	}

	// ── Generate PDF ──────────────────────────────────────────────────────────
	pdfBytes, err := generatePDF(in)
	if err != nil {
		return nil, fmt.Errorf("receipt: generate pdf: %w", err)
	}

	// ── Upload to Spaces ──────────────────────────────────────────────────────
	receiptNumber := buildReceiptNumber(in.Payment)
	storageKey := fmt.Sprintf("receipts/%s/%s.pdf",
		in.Payment.LandlordID, receiptNumber)

	publicURL, err := s.uploader.Upload(ctx, storageKey, pdfBytes)
	if err != nil {
		return nil, fmt.Errorf("receipt: upload: %w", err)
	}

	// ── Persist row ───────────────────────────────────────────────────────────
	rec := models.Receipt{
		PaymentID:     in.Payment.ID,
		LandlordID:    in.Payment.LandlordID,
		UnitID:        in.Payment.UnitID,
		ReceiptNumber: receiptNumber,
		StorageKey:    storageKey,
		PublicURL:     publicURL,
		SentToPhone:   in.Unit.TenantPhone,
	}

	inserted, err := s.repo.InsertReceipt(ctx, rec)
	if err != nil {
		// Non-fatal — the PDF is already uploaded. Log and return URL anyway
		// so the caller can still notify the tenant.
		slog.Error("receipt: insert row failed",
			"payment_id", in.Payment.ID,
			"receipt_number", receiptNumber,
			"error", err,
		)
		inserted = &rec
	}

	return &IssueResult{Receipt: inserted, PublicURL: publicURL}, nil
}

// ── PDF generation ────────────────────────────────────────────────────────────

func generatePDF(in IssueInput) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.AddPage()

	aptName := in.ApartmentName
	if aptName == "" {
		aptName = "RentLoop"
	}

	receiptNo := buildReceiptNumber(in.Payment)
	paidAt := in.Payment.PaidAt
	if paidAt.IsZero() {
		paidAt = time.Now()
	}

	// ── Header ────────────────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 20)
	pdf.SetTextColor(34, 139, 34) // green
	pdf.CellFormat(0, 10, "RentLoop", "", 1, "L", false, 0, "")

	pdf.SetFont("Helvetica", "", 11)
	pdf.SetTextColor(100, 100, 100)
	pdf.CellFormat(0, 6, "Official Rent Receipt", "", 1, "L", false, 0, "")

	pdf.Ln(6)
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(6)

	// ── Receipt metadata ──────────────────────────────────────────────────────
	pdf.SetTextColor(0, 0, 0)
	twoCol(pdf, "Receipt No:", receiptNo)
	twoCol(pdf, "Date:", paidAt.In(eatLoc()).Format("02 Jan 2006"))
	twoCol(pdf, "Period:", formatMonthKey(in.Payment.MonthKey))
	pdf.Ln(4)
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(4)

	// ── Parties ───────────────────────────────────────────────────────────────
	twoCol(pdf, "Property:", aptName)
	twoCol(pdf, "Tenant:", in.Unit.TenantName)
	twoCol(pdf, "Unit:", in.Unit.UnitRef)
	if in.Landlord != nil && in.Landlord.PaybillNumber != "" {
		twoCol(pdf, "Paybill:", in.Landlord.PaybillNumber)
	}
	pdf.Ln(4)
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(4)

	// ── Payment summary ───────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetFillColor(240, 248, 240)
	pdf.CellFormat(0, 10,
		fmt.Sprintf("Amount Paid:  KES %s", formatAmount(in.Payment.Amount)),
		"", 1, "L", true, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.Ln(2)

	statusLabel := strings.Title(strings.ToLower(string(in.Payment.Status)))
	twoCol(pdf, "Status:", statusLabel+" ✓")

	if in.Payment.TransactionID != "" &&
		!strings.HasPrefix(in.Payment.TransactionID, "MANUAL-") {
		twoCol(pdf, "M-Pesa Ref:", in.Payment.TransactionID)
	}

	pdf.Ln(8)
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(4)

	// ── Footer ────────────────────────────────────────────────────────────────
	pdf.SetFont("Helvetica", "I", 9)
	pdf.SetTextColor(150, 150, 150)
	pdf.MultiCell(0, 5,
		"This receipt was generated automatically by RentLoop.\n"+
			"For disputes contact your property manager.",
		"", "C", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// twoCol prints a label + value pair on one line.
func twoCol(pdf *gofpdf.Fpdf, label, value string) {
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(45, 7, label, "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(0, 7, value, "", 1, "L", false, 0, "")
}

// ── Spaces upload ─────────────────────────────────────────────────────────────

func (su *spacesUploader) Upload(ctx context.Context, key string, data []byte) (string, error) {
	// Build a custom endpoint resolver for DigitalOcean Spaces.
	customResolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{URL: su.cfg.SpacesEndpoint}, nil
		},
	)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(su.cfg.SpacesRegion),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			su.cfg.SpacesKey,
			su.cfg.SpacesSecret,
			"",
		)),
	)
	if err != nil {
		return "", fmt.Errorf("spaces: load config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true // required for Spaces
	})

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(su.cfg.SpacesBucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/pdf"),
		ACL:         "public-read",
	})
	if err != nil {
		return "", fmt.Errorf("spaces: put object: %w", err)
	}

	// Build public URL.
	base := su.cfg.CDNBase
	if base == "" {
		base = fmt.Sprintf("https://%s.%s.digitaloceanspaces.com",
			su.cfg.SpacesBucket, su.cfg.SpacesRegion)
	}
	return fmt.Sprintf("%s/%s", strings.TrimRight(base, "/"), key), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// buildReceiptNumber creates a human-readable, sortable receipt number.
// Format: RL-YYYYMMDD-{first 8 chars of payment ID uppercased}
func buildReceiptNumber(p *models.Payment) string {
	date := time.Now().Format("20060102")
	suffix := strings.ToUpper(p.ID)
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return fmt.Sprintf("RL-%s-%s", date, suffix)
}

func formatAmount(n int) string {
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

func formatMonthKey(mk string) string {
	if len(mk) != 7 {
		return mk
	}
	t, err := time.Parse("2006-01", mk)
	if err != nil {
		return mk
	}
	return t.Format("January 2006")
}

func eatLoc() *time.Location {
	loc, _ := time.LoadLocation("Africa/Nairobi")
	if loc == nil {
		return time.UTC
	}
	return loc
}

// IssueAsync generates and uploads a receipt in a background goroutine.
// Callers pass an optional callback to receive the result or log errors.
// This is the preferred call site from the mpesa callback handler so the
// HTTP response is not delayed by PDF generation.
func (s *Service) IssueAsync(in IssueInput, onDone func(result *IssueResult, err error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := s.Issue(ctx, in)
		if onDone != nil {
			onDone(result, err)
		}
	}()
}

// Dummy io import guard (used by upload via bytes.NewReader which satisfies io.Reader)
var _ io.Reader = (*bytes.Reader)(nil)
