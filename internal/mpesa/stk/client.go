// Package stk implements the Safaricom Daraja STK Push (Lipa Na M-Pesa Online) client.
package stk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	sandboxBase    = "https://sandbox.safaricom.co.ke"
	productionBase = "https://api.safaricom.co.ke"
)

// Client handles OAuth + STK Push against Daraja.
type Client struct {
	shortcode   string
	passkey     string
	consumerKey string
	consumerSec string
	callbackURL string
	baseURL     string
	http        *http.Client
}

// NewClient reads config from environment and returns a ready client.
// Set APP_ENV=production to hit the live Daraja API.
func NewClient() *Client {
	base := sandboxBase
	if os.Getenv("APP_ENV") == "production" {
		base = productionBase
	}
	return &Client{
		shortcode:   os.Getenv("MPESA_SHORTCODE"),
		passkey:     os.Getenv("MPESA_PASSKEY"),
		consumerKey: os.Getenv("MPESA_CONSUMER_KEY"),
		consumerSec: os.Getenv("MPESA_CONSUMER_SECRET"),
		callbackURL: os.Getenv("MPESA_CALLBACK_URL"),
		baseURL:     base,
		http:        &http.Client{Timeout: 15 * time.Second},
	}
}

// Push initiates an STK Push to the given phone number.
// phone must be in international format without '+': e.g. "254712345678".
// accountRef appears on the customer's M-Pesa confirmation SMS.
func (c *Client) Push(ctx context.Context, phone string, amount int, accountRef string) error {
	token, err := c.token(ctx)
	if err != nil {
		return fmt.Errorf("stk: oauth: %w", err)
	}

	timestamp := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString(
		[]byte(c.shortcode + c.passkey + timestamp),
	)

	payload := map[string]any{
		"BusinessShortCode": c.shortcode,
		"Password":          password,
		"Timestamp":         timestamp,
		"TransactionType":   "CustomerPayBillOnline",
		"Amount":            amount,
		"PartyA":            phone,
		"PartyB":            c.shortcode,
		"PhoneNumber":       phone,
		"CallBackURL":       c.callbackURL,
		"AccountReference":  accountRef,
		"TransactionDesc":   "RentLoop subscription",
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/mpesa/stkpush/v1/processrequest", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("stk: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stk: push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stk: daraja %d: %s", resp.StatusCode, raw)
	}

	var result struct {
		ResponseCode string `json:"ResponseCode"`
		ResponseDesc string `json:"ResponseDescription"`
		CheckoutID   string `json:"CheckoutRequestID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("stk: decode response: %w", err)
	}
	if result.ResponseCode != "0" {
		return fmt.Errorf("stk: daraja rejected: %s", result.ResponseDesc)
	}
	return nil
}

// token fetches a short-lived OAuth2 bearer token from Daraja.
func (c *Client) token(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/oauth/v1/generate?grant_type=client_credentials", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.consumerKey, c.consumerSec)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("empty access token")
	}
	return tok.AccessToken, nil

}
