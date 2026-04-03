package mpesa

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// STKCallbackBody is the top-level shape Safaricom POSTs to your callback URL.
type STKCallbackBody struct {
	Body struct {
		STKCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  *struct {
				Item []struct {
					Name  string `json:"Name"`
					Value any    `json:"Value"`
				} `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// STKCallback handles POST /mpesa/stk/callback.
func (h *Handler) STKCallback(w http.ResponseWriter, r *http.Request) {
	var body STKCallbackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		slog.Warn("stk callback: malformed JSON", "error", err)
		writeJSON(w, http.StatusBadRequest, C2BResponse{ResultCode: "1", ResultDesc: "Bad Request"})
		return
	}

	cb := body.Body.STKCallback
	log := slog.With("checkout_id", cb.CheckoutRequestID, "result_code", cb.ResultCode)

	// Always acknowledge Safaricom immediately.
	writeJSON(w, http.StatusOK, C2BResponse{ResultCode: "0", ResultDesc: "Accepted"})

	if cb.ResultCode != 0 {
		log.Warn("stk callback: customer cancelled or timed out", "desc", cb.ResultDesc)
		return
	}

	// Extract receipt and amount from CallbackMetadata.
	var receipt string
	var amount int

	if cb.CallbackMetadata != nil {
		for _, item := range cb.CallbackMetadata.Item {
			switch item.Name {
			case "MpesaReceiptNumber":
				if v, ok := item.Value.(string); ok {
					receipt = v
				}
			case "Amount":
				switch v := item.Value.(type) {
				case float64:
					amount = int(v)
				case int:
					amount = v
				}
			}
		}
	}

	if receipt == "" || amount == 0 {
		log.Error("stk callback: missing receipt or amount in metadata")
		return
	}

	log.Info("stk callback: success", "receipt", receipt, "amount", amount)

	if h.billing != nil {
		h.billing.ProcessSubscriptionFromSTK(cb.CheckoutRequestID, receipt, amount)
	}
}
