package main

import (
	"context"
	"log"

	"github.com/codercollo/rentloop/internal/notifier"
)

func main() {
	sms := notifier.NewSMS(
		"atsk_071e7c76bee510bd92b20dcb568028aee51dc35d29c8f70de06b38a0682740e13a4e8691", // sandbox API key
		"sandbox",
		"RentLoop", // sender ID
	)

	// One-off SMS for sandbox activation
	err := sms.SendActivationSMS(context.Background(), "+254741775492", "Sandbox Activation Test via SMS")
	if err != nil {
		log.Fatal("failed:", err)
	}

	log.Println("SMS sent — sandbox should now be active")
}
