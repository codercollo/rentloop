// Package config loads and validates application configuration from .env
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds every value the application needs.
// All fields are populated from environment variables.
type Config struct {
	// App
	Port   string
	AppEnv string // development | production

	// Database
	DatabaseURL string

	// M-Pesa daraja
	MpesaEnv            string
	MpesaConsumerKey    string
	MpesaConsumerSecret string
	MpesaPaybill        string
	MpesaPasskey        string

	// Africa's Talking
	ATAPIKey         string
	ATUsername       string
	ATWhatsAppNumber string
	ATSMSSender      string

	// Twilio
	TwilioSID          string
	TwilioToken        string
	TwilioWhatsAppFrom string
	TwilioSMSFrom      string

	// Twilio webhook URLs — used for signature validation.
	// Set these to the full public URL Twilio posts to, e.g.
	// https://rentloop.co.ke/bot/whatsapp
	WhatsAppWebhookURL string
	SMSWebhookURL      string

	// Admin auth
	JWTSecret        string
	ActivationSecret string

	// DigitalOcean Spaces
	DOSpacesKey      string
	DOSpacesSecret   string
	DOSpacesBucket   string
	DOSpacesRegion   string
	DOSpacesEndpoint string

	// Billing
	BillingGraceDays         int
	SubscriptionPricePerUnit int
	FreeTierUnitLimit        int

	// Admin setup
	AdminSetupSecret string
}

// Load reads environment variables, validates required fields,
// and returns a populated Config or an error.
func Load() (*Config, error) {
	cfg := &Config{
		Port:   getEnv("PORT", "8080"),
		AppEnv: getEnv("APP_ENV", "development"),

		DatabaseURL: os.Getenv("DATABASE_URL"),

		MpesaEnv:            getEnv("MPESA_ENV", "sandbox"),
		MpesaConsumerKey:    os.Getenv("MPESA_CONSUMER_KEY"),
		MpesaConsumerSecret: os.Getenv("MPESA_CONSUMER_SECRET"),
		MpesaPaybill:        os.Getenv("MPESA_PAYBILL"),
		MpesaPasskey:        os.Getenv("MPESA_PASSKEY"),

		ATAPIKey:         os.Getenv("AT_API_KEY"),
		ATUsername:       os.Getenv("AT_USERNAME"),
		ATWhatsAppNumber: os.Getenv("AT_WHATSAPP_NUMBER"),
		ATSMSSender:      os.Getenv("AT_SMS_SENDER_ID"),

		TwilioSID:          os.Getenv("TWILIO_SID"),
		TwilioToken:        os.Getenv("TWILIO_TOKEN"),
		TwilioWhatsAppFrom: getEnv("TWILIO_WHATSAPP_FROM", "whatsapp:+14155238886"),
		TwilioSMSFrom:      getEnv("TWILIO_SMS_FROM", "+14155238886"),

		WhatsAppWebhookURL: getEnv("WHATSAPP_WEBHOOK_URL", ""),
		SMSWebhookURL:      getEnv("SMS_WEBHOOK_URL", ""),

		JWTSecret:        os.Getenv("JWT_SECRET"),
		ActivationSecret: os.Getenv("ACTIVATION_SECRET"),

		DOSpacesKey:      os.Getenv("DO_SPACES_KEY"),
		DOSpacesSecret:   os.Getenv("DO_SPACES_SECRET"),
		DOSpacesBucket:   os.Getenv("DO_SPACES_BUCKET"),
		DOSpacesRegion:   os.Getenv("DO_SPACES_REGION"),
		DOSpacesEndpoint: os.Getenv("DO_SPACES_ENDPOINT"),

		BillingGraceDays:         getEnvInt("BILLING_GRACE_DAYS", 5),
		SubscriptionPricePerUnit: getEnvInt("SUBSCRIPTION_PRICE_PER_UNIT", 50),
		FreeTierUnitLimit:        getEnvInt("FREE_TIER_UNIT_LIMIT", 10),

		AdminSetupSecret: os.Getenv("ADMIN_SETUP_SECRET"),
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// IsDevelopment returns true when running outside production.
func (c *Config) IsDevelopment() bool {
	return c.AppEnv != "production"
}

// IsProduction returns true in the production environment.
func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}

// validate checks all required fields and returns a combined error.
func (c *Config) validate() error {
	required := map[string]string{
		"DATABASE_URL":          c.DatabaseURL,
		"MPESA_CONSUMER_KEY":    c.MpesaConsumerKey,
		"MPESA_CONSUMER_SECRET": c.MpesaConsumerSecret,
		"MPESA_PAYBILL":         c.MpesaPaybill,
		"MPESA_PASSKEY":         c.MpesaPasskey,
		"AT_API_KEY":            c.ATAPIKey,
		"AT_USERNAME":           c.ATUsername,
		"AT_WHATSAPP_NUMBER":    c.ATWhatsAppNumber,
		"AT_SMS_SENDER_ID":      c.ATSMSSender,
		"JWT_SECRET":            c.JWTSecret,
		"ACTIVATION_SECRET":     c.ActivationSecret,
		"DO_SPACES_KEY":         c.DOSpacesKey,
		"DO_SPACES_SECRET":      c.DOSpacesSecret,
		"DO_SPACES_BUCKET":      c.DOSpacesBucket,
		"DO_SPACES_REGION":      c.DOSpacesRegion,
		"DO_SPACES_ENDPOINT":    c.DOSpacesEndpoint,
	}

	// Webhook URLs are only required in production — in development
	// ValidateTwilio is skipped entirely so blank values are fine.
	if c.IsProduction() {
		required["WHATSAPP_WEBHOOK_URL"] = c.WhatsAppWebhookURL
		required["SMS_WEBHOOK_URL"] = c.SMSWebhookURL
	}

	var missing []string
	for key, val := range required {
		if strings.TrimSpace(val) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 characters")
	}
	return nil
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return n
}
