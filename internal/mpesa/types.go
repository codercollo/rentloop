// Package mpesa defines data structures used when interacting with
// Safaricom Daraja C2B APIs.
//
// It includes request and response payloads for callback handling,
// URL registration, and sandbox simulation. Field names and JSON tags
// must match the Daraja specification exactly.

package mpesa

// C2BCallback is the exact payload Safaricom Daraja POSTs
// to your confirmation URL on every C2B payment.
// Field names match the Daraja API spec exactly — do not rename them.
type C2BCallback struct {
	TransactionType   string `json:"TransactionType"`
	TransID           string `json:"TransID"`           // unique M-Pesa transaction ID
	TransTime         string `json:"TransTime"`         // YYYYMMDDHHmmss
	TransAmount       string `json:"TransAmount"`       // string e.g. "12500.00"
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
