// Package main starts the RentLoop HTTP server.
//
// It loads configuration, initializes the database, wires all
// internal dependencies, mounts routes, and runs the server
// with graceful shutdown on SIGINT/SIGTERM.
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
	// ── Logger ────────────────────────────────────────────────────────────────
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// ── Database ──────────────────────────────────────────────────────────────
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connection established")

	// ── Repositories ──────────────────────────────────────────────────────────
	// ledgerRepo satisfies three interfaces:
	//   ledger.PaymentRepository  — InsertPayment, GetMonthlyTotal, GetUnit
	//   matcher.Repository        — GetUnitByRef
	//   mpesa.LandlordRepository  — GetByPaybill
	ledgerRepo := ledger.NewRepository(pool)
	botRepo := bot.NewRepository(pool)

	// ── Services ──────────────────────────────────────────────────────────────
	ledgerSvc := ledger.NewService(ledgerRepo)
	matcherSvc := matcher.New(ledgerRepo)

	// ── Notifiers ─────────────────────────────────────────────────────────────
	waSvc := notifier.NewWhatsApp(cfg.ATAPIKey, cfg.ATUsername, cfg.ATWhatsAppNumber)
	smsSvc := notifier.NewSMS(cfg.ATAPIKey, cfg.ATUsername, cfg.ATSMSSender)

	// combinedNotifier satisfies mpesa.NotifierService
	notify := &combinedNotifier{wa: waSvc, sms: smsSvc}

	// botSender adapts *notifier.WhatsApp to bot.Sender (plain Send method)
	waSender := &botSender{wa: waSvc}

	// ── Bot ───────────────────────────────────────────────────────────────────
	botSvc := bot.NewService(botRepo, waSender, smsSvc)
	botHandler := bot.NewHandler(botSvc)

	onboardingRepo := botRepo
	onboardingSvc := onboarding.NewService(onboardingRepo, waSender, smsSvc)
	botSvc.SetOnboarding(onboardingSvc)

	// ── Cron — 6 PM daily digest ──────────────────────────────────────────────
	c := cron.New()
	if err := botSvc.StartDigest(c); err != nil {
		slog.Error("failed to register digest cron", "error", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()

	// ── M-Pesa handler ────────────────────────────────────────────────────────
	mpesaHandler := mpesa.NewHandler(
		matcherSvc,
		ledgerSvc,
		notify,
		ledgerRepo,
		cfg.IsDevelopment(),
	)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger)
	r.Use(middleware.Timeout(30 * time.Second))

	// Health check — unauthenticated, used by CI deploy workflow
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// M-Pesa C2B payment callback
	r.Post("/mpesa/c2b/callback", mpesaHandler.Callback)

	// WhatsApp bot inbound messages
	r.Post("/bot/whatsapp", botHandler.Inbound)

	// Mounted as each package is built:
	// r.Mount("/billing", billingHandler.Routes())
	// r.Mount("/admin",   adminHandler.Routes())

	// ── HTTP Server ───────────────────────────────────────────────────────────
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

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case sig := <-quit:
		slog.Info("shutdown signal received", "signal", sig)
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

// combinedNotifier adapts *notifier.WhatsApp and *notifier.SMS into the
// single mpesa.NotifierService interface.
type combinedNotifier struct {
	wa  *notifier.WhatsApp
	sms *notifier.SMS
}

func (n *combinedNotifier) NotifyLandlord(ctx context.Context, landlordPhone string, p *models.Payment, unit *models.Unit) error {
	return n.wa.NotifyLandlord(ctx, landlordPhone, p, unit)
}

func (n *combinedNotifier) NotifyTenant(ctx context.Context, phone string, p *models.Payment, unit *models.Unit) error {
	return n.sms.NotifyTenant(ctx, phone, p, unit)
}

// botSender adapts *notifier.WhatsApp to bot.Sender.
// The bot only needs a plain Send(to, message) — it does not need
// the structured NotifyLandlord/NotifyTenant methods.
type botSender struct {
	wa *notifier.WhatsApp
}

func (b *botSender) Send(ctx context.Context, to, message string) error {
	return b.wa.SendRaw(ctx, to, message)
}

// ── Middleware ────────────────────────────────────────────────────────────────

// requestLogger logs HTTP request metadata using structured slog.
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
