// Package mpesa provides validation and utility helpers for handling
// Safaricom Daraja C2B callbacks.
//
// It includes IP validation, payload validation, and parsing helpers
// required by the HTTP handler to safely process inbound payments.
package mpesa

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// safaricomCIDRs contains the IP ranges Safaricom uses to deliver
// Daraja callbacks. Requests from any other IP are rejected with 403.
//
// Sandbox:  196.201.214.0/24  — Safaricom developer platform
// Production ranges published at developer.safaricom.co.ke
var safaricomCIDRs = []string{
	// Sandbox
	"196.201.214.0/24",
	"196.201.214.200/32",
	// Production
	"196.201.214.0/24",
	"196.201.214.200/32",
	"196.201.212.0/23",
	"192.168.201.0/24",
}

var allowedNetworks []*net.IPNet

func init() {
	for _, cidr := range safaricomCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid Safaricom CIDR %q: %v", cidr, err))
		}
		allowedNetworks = append(allowedNetworks, network)
	}
}

// ValidateIP checks whether the request originates from a known
// Safaricom IP range. Returns an error if the IP is not allowed.
// In development mode this check is bypassed to allow ngrok and
// local testing — never bypass in production.
func ValidateIP(r *http.Request, isDevelopment bool) error {
	if isDevelopment {
		return nil
	}

	ip, err := extractIP(r)
	if err != nil {
		return fmt.Errorf("extract IP: %w", err)
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("unparseable IP: %s", ip)
	}

	for _, network := range allowedNetworks {
		if network.Contains(parsed) {
			return nil
		}
	}

	return fmt.Errorf("IP %s is not a known Safaricom address", ip)
}

// extractIP reads the real client IP, respecting X-Forwarded-For
// set by Nginx when running behind a reverse proxy.
func extractIP(r *http.Request) (string, error) {
	// X-Forwarded-For may contain a comma-separated chain; take the first
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip, nil
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without port
		return r.RemoteAddr, nil
	}
	return ip, nil
}

// ValidatePayload checks all required fields are present and the
// amount can be parsed as a positive number.
func ValidatePayload(cb *C2BCallback) error {
	var missing []string

	if strings.TrimSpace(cb.TransID) == "" {
		missing = append(missing, "TransID")
	}
	if strings.TrimSpace(cb.TransAmount) == "" {
		missing = append(missing, "TransAmount")
	}
	if strings.TrimSpace(cb.BillRefNumber) == "" {
		missing = append(missing, "BillRefNumber")
	}
	if strings.TrimSpace(cb.BusinessShortCode) == "" {
		missing = append(missing, "BusinessShortCode")
	}
	if strings.TrimSpace(cb.MSISDN) == "" {
		missing = append(missing, "MSISDN")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}

	amount, err := strconv.ParseFloat(cb.TransAmount, 64)
	if err != nil {
		return fmt.Errorf("TransAmount %q is not a valid number", cb.TransAmount)
	}
	if amount <= 0 {
		return fmt.Errorf("TransAmount must be greater than zero, got %v", amount)
	}

	return nil
}

// ParseAmount converts Safaricom's string amount "12500.00" to
// an integer in KES (whole shillings, no cents).
func ParseAmount(s string) (int, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("parse amount %q: %w", s, err)
	}
	return int(f), nil
}
