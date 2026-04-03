package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	chi "github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/robfig/cron/v3"

	"github.com/codercollo/rentloop/internal/admin"
	"github.com/codercollo/rentloop/internal/auth"
	"github.com/codercollo/rentloop/internal/billing"
	"github.com/codercollo/rentloop/internal/bot"
	"github.com/codercollo/rentloop/internal/config"
	"github.com/codercollo/rentloop/internal/db"
	deposits "github.com/codercollo/rentloop/internal/deposit"
	"github.com/codercollo/rentloop/internal/ledger"
	"github.com/codercollo/rentloop/internal/matcher"
	appMiddleware "github.com/codercollo/rentloop/internal/middleware"
	"github.com/codercollo/rentloop/internal/models"
	"github.com/codercollo/rentloop/internal/mpesa"
	"github.com/codercollo/rentloop/internal/mpesa/stk"
	"github.com/codercollo/rentloop/internal/notifier"
	"github.com/codercollo/rentloop/internal/onboarding"
	receiptpkg "github.com/codercollo/rentloop/internal/receipt"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connection established")

	// ── Templates ─────────────────────────────────────────────────────────────
	tmpl := template.Must(template.ParseGlob("web/templates/*.html"))

	// ── Repositories ──────────────────────────────────────────────────────────
	ledgerRepo := ledger.NewRepository(pool)
	botRepo := bot.NewRepository(pool)
	depositRepo := deposits.NewRepository(pool)
	receiptRepo := receiptpkg.NewRepository(pool)

	// ── Core services ─────────────────────────────────────────────────────────
	ledgerSvc := ledger.NewService(ledgerRepo)
	matcherSvc := matcher.New(ledgerRepo)

	// ── Deposit service ───────────────────────────────────────────────────────
	depositSvc := deposits.NewService(depositRepo, ledgerRepo)

	// ── Receipt service ───────────────────────────────────────────────────────
	// receiptSvc := receiptpkg.NewService(receiptpkg.Config{
	// 	SpacesKey:      cfg.DOSpacesKey,
	// 	SpacesSecret:   cfg.DOSpacesSecret,
	// 	SpacesBucket:   cfg.DOSpacesBucket,
	// 	SpacesRegion:   cfg.DOSpacesRegion,
	// 	SpacesEndpoint: cfg.DOSpacesEndpoint,
	// }, receiptRepo)

	// ── Receipt service ───────────────────────────────────────────────────────────
	receiptCfg := receiptpkg.Config{
		SpacesKey:      cfg.DOSpacesKey,
		SpacesSecret:   cfg.DOSpacesSecret,
		SpacesBucket:   cfg.DOSpacesBucket,
		SpacesRegion:   cfg.DOSpacesRegion,
		SpacesEndpoint: cfg.DOSpacesEndpoint,
	}

	var receiptSvc *receiptpkg.Service
	if cfg.IsDevelopment() {
		receiptSvc = receiptpkg.NewServiceWithUploader(receiptCfg, receiptRepo, &fakeUploader{})
		slog.Info("receipt: using fake uploader (dev mode)")
	} else {
		receiptSvc = receiptpkg.NewService(receiptCfg, receiptRepo)
	}

	// ── Twilio — WhatsApp + SMS ───────────────────────────────────────────────
	tw := notifier.NewTwilio(
		cfg.TwilioSID,
		cfg.TwilioToken,
		cfg.TwilioWhatsAppFrom,
		cfg.TwilioSMSFrom,
	)

	// ── Bot ───────────────────────────────────────────────────────────────────
	botSvc := bot.NewService(botRepo, &waSender{tw}, tw)
	botSvc.SetDeposits(depositSvc)
	botHandler := bot.NewHandler(botSvc)
	smsHandler := bot.NewSMSHandler(botSvc)

	onboardingSvc := onboarding.NewService(botRepo, &smsSender{tw}, tw, os.Getenv("OWNER_PHONE"))
	botSvc.SetOnboarding(onboardingSvc)

	// ── Billing ───────────────────────────────────────────────────────────────
	billingRepo := billing.NewRepository(pool)
	billingSvc := billing.NewService(
		billingRepo,
		&waSender{tw},
		cfg.BillingGraceDays,
		cfg.SubscriptionPricePerUnit,
		cfg.FreeTierUnitLimit,
	)
	billingHandler := billing.NewHandler(billingSvc)

	stkClient := stk.NewClient()
	billingHandler.SetSTKClient(stkClient)

	// ── Auth ──────────────────────────────────────────────────────────────────
	authRepo := auth.NewRepository(pool)
	authSvc := auth.NewService(authRepo, cfg.JWTSecret, cfg.ActivationSecret)
	authHandler := auth.NewHandler(authSvc, tmpl)

	// ── Admin ─────────────────────────────────────────────────────────────────
	adminRepo := admin.NewRepository(pool)
	adminHandler := admin.NewHandler(adminRepo, tmpl)

	// ── Cron ──────────────────────────────────────────────────────────────────
	c := cron.New()
	if err := botSvc.StartDigest(c); err != nil {
		slog.Error("digest cron", "error", err)
		os.Exit(1)
	}
	if err := billingSvc.StartCron(c); err != nil {
		slog.Error("billing cron", "error", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()

	// ── M-Pesa ────────────────────────────────────────────────────────────────
	// mpesaHandler must be declared BEFORE SetReceiptService is called.
	mpesaHandler := mpesa.NewHandler(
		matcherSvc,
		ledgerSvc,
		&paymentNotifier{tw},
		ledgerRepo,
		billingHandler,
		cfg.IsDevelopment(),
	)
	mpesaHandler.SetReceiptService(receiptSvc)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(requestLogger)
	r.Use(chimw.Timeout(30 * time.Second))
	r.Use(appMiddleware.CORS(appMiddleware.CORSOptions{
		AllowedOrigins: []string{"https://rentloop.co.ke"},
	}))

	// ── Public routes ─────────────────────────────────────────────────────────
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl.ExecuteTemplate(w, "landing.html", map[string]any{
			"Year": time.Now().Year(),
			"Commands": []struct{ Cmd, Desc string }{
				{"LIST", "Paid vs unpaid this month"},
				{"REMIND", "SMS all unpaid tenants"},
				{"TOTAL", "Collected vs expected"},
				{"RECEIPT 4B", "Resend receipt for a unit"},
				{"HISTORY 4B", "Last 3 months for a unit"},
				{"HISTORY-EXT 4B", "Full 12-month history with arrears"},
				{"LANDLORD-HISTORY", "12-month portfolio performance"},
				{"DEPOSIT 4B", "Deposit balance for a unit"},
				{"DEPOSIT-REFUND 4B 15000", "Record deposit refund"},
				{"MARK 4B PAID 12500 BANK", "Log a cash/bank payment"},
				{"CLAIM TXN-123 TO 4B", "Assign unmatched payment"},
				{"ADD UNIT 4B John 0712345678 12500", "Add a unit"},
			},
		})
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServer(http.Dir("web/static"))))

	// ── Webhooks (public — no auth required) ──────────────────────────────────
	whatsappValidate := appMiddleware.ValidateTwilio(cfg.TwilioToken, cfg.WhatsAppWebhookURL, cfg.IsDevelopment())
	smsValidate := appMiddleware.ValidateTwilio(cfg.TwilioToken, cfg.SMSWebhookURL, cfg.IsDevelopment())

	r.With(appMiddleware.WhatsAppRateLimit, whatsappValidate).Post("/bot/whatsapp", botHandler.Inbound)
	r.With(appMiddleware.SMSRateLimit, smsValidate).Post("/bot/sms", smsHandler.Inbound)
	r.With(appMiddleware.MpesaRateLimit).Post("/mpesa/c2b/callback", mpesaHandler.Callback)
	r.With(appMiddleware.MpesaRateLimit).Post("/mpesa/stk/callback", mpesaHandler.STKCallback)

	// ── Admin auth — public ───────────────────────────────────────────────────
	r.Get("/admin/login", authHandler.ShowLogin)
	r.Post("/admin/login", authHandler.Login)
	r.Get("/admin/activate", authHandler.Activate)
	r.Post("/admin/logout", authHandler.Logout)
	r.Get("/admin/setup", adminHandler.ShowSetup(cfg.AdminSetupSecret, authSvc))
	r.Post("/admin/setup", adminHandler.DoSetup(cfg.AdminSetupSecret, authSvc))

	// ── Admin dashboard — JWT protected ──────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(appMiddleware.RequireAdmin(authSvc))

		r.Get("/admin/dashboard", adminHandler.Dashboard)
		r.Get("/admin/clients", adminHandler.Clients)
		r.Get("/admin/clients/{id}", adminHandler.ClientDetail)
		r.Post("/admin/clients/{id}/activate", adminHandler.ActivateClient)
		r.Get("/admin/agents", adminHandler.Agents)
		r.Get("/admin/payments", adminHandler.Payments)
		r.Get("/admin/payments/unmatched", adminHandler.UnmatchedPayments)

		r.Get("/admin/payments/partial", func(w http.ResponseWriter, r *http.Request) {
			payments, _ := adminRepo.GetRecentPayments(r.Context())
			tmpl.ExecuteTemplate(w, "admin_payments_partial.html", payments)
		})
	})

	// ── Internal — STK billing ────────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(appMiddleware.RequireInternal)
		r.Post("/billing/stk", billingHandler.TriggerSTK)
	})

	// ── Server ────────────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("server starting", "port", cfg.Port, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case sig := <-quit:
		slog.Info("shutdown", "signal", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped cleanly")
}

// ── Adapters ──────────────────────────────────────────────────────────────────

type paymentNotifier struct{ tw *notifier.Twilio }

func (n *paymentNotifier) NotifyLandlord(ctx context.Context, phone string, p *models.Payment, unit *models.Unit, apartmentName string) error {
	return n.tw.NotifyLandlord(ctx, phone, p, unit, apartmentName)
}

func (n *paymentNotifier) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit, apartmentName string) error {
	return n.tw.NotifyTenant(ctx, phone, p, unit, apartmentName)
}

type waSender struct{ tw *notifier.Twilio }

func (b *waSender) Send(ctx context.Context, to, msg string) error {
	return b.tw.SendRaw(ctx, to, msg)
}

type smsSender struct{ tw *notifier.Twilio }

func (s *smsSender) Send(ctx context.Context, to, msg string) error {
	return s.tw.SendRawSMS(ctx, to, msg)

}

type fakeUploader struct{}

func (f *fakeUploader) Upload(_ context.Context, key string, _ []byte) (string, error) {
	return "https://fake.spaces.example/" + key, nil
}

// ── Middleware ────────────────────────────────────────────────────────────────

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", chimw.GetReqID(r.Context()),
		)
	})
}
