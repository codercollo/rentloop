package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/robfig/cron/v3"

	"github.com/codercollo/rentloop/internal/bot"
	"github.com/codercollo/rentloop/internal/config"
	"github.com/codercollo/rentloop/internal/db"
	"github.com/codercollo/rentloop/internal/ledger"
	"github.com/codercollo/rentloop/internal/matcher"
	"github.com/codercollo/rentloop/internal/models"
	"github.com/codercollo/rentloop/internal/mpesa"
	"github.com/codercollo/rentloop/internal/notifier"
	"github.com/codercollo/rentloop/internal/onboarding"
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

	// ── Repositories ──────────────────────────────────────────────────────────
	// ledgerRepo satisfies ledger.PaymentRepository, matcher.Repository,
	// and mpesa.LandlordRepository — one struct, three interfaces.
	ledgerRepo := ledger.NewRepository(pool)
	botRepo := bot.NewRepository(pool)

	// ── Services ──────────────────────────────────────────────────────────────
	ledgerSvc := ledger.NewService(ledgerRepo)
	matcherSvc := matcher.New(ledgerRepo)

	// ── Twilio — WhatsApp + SMS ───────────────────────────────────────────────
	// Single client handles both channels.
	// WhatsApp → landlord notifications + bot replies
	// SMS      → tenant receipts, reminders, onboarding
	tw := notifier.NewTwilio(
		cfg.TwilioSID,
		cfg.TwilioToken,
		cfg.TwilioWhatsAppFrom,
		cfg.TwilioSMSFrom,
	)

	// ── Bot ───────────────────────────────────────────────────────────────────
	// waSender  → bot replies go via Twilio WhatsApp
	// smsSender → onboarding welcome messages go via Twilio SMS
	botSvc := bot.NewService(botRepo, &waSender{tw}, tw)
	botHandler := bot.NewHandler(botSvc)
	smsHandler := bot.NewSMSHandler(botSvc)

	onboardingSvc := onboarding.NewService(botRepo, &smsSender{tw}, tw)
	botSvc.SetOnboarding(onboardingSvc)

	// ── Cron ─────────────────────────────────────────────────────────────────
	c := cron.New()
	if err := botSvc.StartDigest(c); err != nil {
		slog.Error("cron", "error", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()

	// ── M-Pesa ───────────────────────────────────────────────────────────────
	mpesaHandler := mpesa.NewHandler(
		matcherSvc,
		ledgerSvc,
		&paymentNotifier{tw},
		ledgerRepo,
		cfg.IsDevelopment(),
	)

	// ── Router ───────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	r.Post("/mpesa/c2b/callback", mpesaHandler.Callback)
	r.Post("/bot/whatsapp", botHandler.Inbound)
	r.Post("/bot/sms", smsHandler.Inbound)

	// pending: /billing, /admin

	// ── Server ───────────────────────────────────────────────────────────────
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

// ── Adapters ─────────────────────────────────────────────────────────────────

// paymentNotifier satisfies mpesa.NotifierService.
// Landlord → Twilio WhatsApp. Tenant → Twilio SMS.
type paymentNotifier struct{ tw *notifier.Twilio }

func (n *paymentNotifier) NotifyLandlord(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	return n.tw.NotifyLandlord(ctx, phone, p, unit)
}

func (n *paymentNotifier) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	return n.tw.NotifyTenant(ctx, phone, p, unit)
}

// waSender adapts Twilio to bot.Sender — bot replies via WhatsApp.
type waSender struct{ tw *notifier.Twilio }

func (b *waSender) Send(ctx context.Context, to, msg string) error {
	return b.tw.SendRaw(ctx, to, msg)
}

// smsSender adapts Twilio to bot.Sender — onboarding replies via SMS.
type smsSender struct{ tw *notifier.Twilio }

func (s *smsSender) Send(ctx context.Context, to, msg string) error {
	return s.tw.SendRawSMS(ctx, to, msg)
}

// ── Middleware ────────────────────────────────────────────────────────────────

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}
