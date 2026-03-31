// Package mpesa defines data structures used when interacting with
// Safaricom Daraja C2B APIs.
//
// It includes request and response payloads for callback handling,
// URL registration, and sandbox simulation. Field names and JSON tags
// must match the Daraja specification exactly.

package mpesa

import (
	"strings"

	"github.com/codercollo/rentloop/internal/models"
)

// C2BCallback is the exact payload Safaricom Daraja POSTs
// to your confirmation URL on every C2B payment.
// Field names match the Daraja API spec exactly — do not rename them.
type C2BCallback struct {
	TransactionType   string `json:"TransactionType"`
	TransID           string `json:"TransID"` // unique M-Pesa transaction ID
	TransTime         string `json:"TransTime"`
	TransAmount       string `json:"TransAmount"`
	BusinessShortCode string `json:"BusinessShortCode"` // your paybill number
	BillRefNumber     string `json:"BillRefNumber"`     // account reference — tenant unit e.g. "4B"
	InvoiceNumber     string `json:"InvoiceNumber"`
	OrgAccountBalance string `json:"OrgAccountBalance"`
	ThirdPartyTransID string `json:"ThirdPartyTransID"`
	MSISDN            string `json:"MSISDN"` // tenant phone (may be masked)
	FirstName         string `json:"FirstName"`
	MiddleName        string `json:"MiddleName"`
	LastName          string `json:"LastName"`
}

// C2BResponse is returned to Safaricom immediately.
// Daraja expects ResultCode "0" for success.
// Any other code or non-200 HTTP status causes a retry.
type C2BResponse struct {
	ResultCode string `json:"ResultCode"`
	ResultDesc string `json:"ResultDesc"`
}

// RegisterURLRequest is sent once to tell Daraja where to POST callbacks.
type RegisterURLRequest struct {
	ShortCode       string `json:"ShortCode"`
	ResponseType    string `json:"ResponseType"` // "Completed" or "Cancelled"
	ConfirmationURL string `json:"ConfirmationURL"`
	ValidationURL   string `json:"ValidationURL"`
}

// RegisterURLResponse is Safaricom's response to URL registration.
type RegisterURLResponse struct {
	OriginatorConversationID string `json:"OriginatorConversationID"`
	ResponseCode             string `json:"ResponseCode"`
	ResponseDescription      string `json:"ResponseDescription"`
}

// SimulateRequest is used in sandbox testing only.
type SimulateRequest struct {
	ShortCode     string `json:"ShortCode"`
	CommandID     string `json:"CommandID"` // "CustomerPayBillOnline"
	Amount        string `json:"Amount"`
	Msisdn        string `json:"Msisdn"`
	BillRefNumber string `json:"BillRefNumber"` // unit ref e.g. "4B"
}

// successResponse is the standard accepted response body.
var successResponse = C2BResponse{
	ResultCode: "0",
	ResultDesc: "Accepted",
}

// knownBankMSISDNPrefixes contains MSISDN prefixes used by bank aggregators.
var knownBankMSISDNPrefixes = []string{
	"254100", // Equity Bank
	"254101",
	"254200", // KCB
	"254201",
	"254300", // Co-op Bank
}

// DetectPaymentSource determines whether the callback originated from a
// personal M-Pesa wallet (STK) or a bank Paybill app.
func DetectPaymentSource(cb C2BCallback) models.PaymentSource {
	// Safaricom populates ThirdPartyTransID for bank-initiated Paybill flows.
	if strings.TrimSpace(cb.ThirdPartyTransID) != "" {
		return models.PaymentSourceBankPaybill
	}

	msisdn := strings.TrimSpace(cb.MSISDN)

	// No MSISDN — cannot be a personal wallet push.
	if msisdn == "" {
		return models.PaymentSourceBankPaybill
	}

	for _, prefix := range knownBankMSISDNPrefixes {
		if strings.HasPrefix(msisdn, prefix) {
			return models.PaymentSourceBankPaybill
		}
	}

	return models.PaymentSourceMpesaSTK
}
