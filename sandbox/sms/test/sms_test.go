package main

import (
	"context"
	"log"
	"time"

	"github.com/codercollo/rentloop/internal/models"
	"github.com/codercollo/rentloop/internal/notifier"
)

func main() {
	sms := notifier.NewSMS(
		"atsk_071e7c76bee510bd92b20dcb568028aee51dc35d29c8f70de06b38a0682740e13a4e8691",
		"sandbox",
		"RentLoop",
	)

	// Simulate a payment
	payment := &models.Payment{
		ID:            "PAY12345678",
		Amount:        15000,
		PaidAt:        time.Now(),
		TransactionID: "TXN987654321",
		Status:        models.PaymentStatusPaid,
		TenantPhone:   "+254741775492",
	}

	unit := &models.Unit{
		UnitRef:    "A101",
		TenantName: "John Doe",
	}

	// Send tenant notification
	err := sms.NotifyTenant(context.Background(), payment.TenantPhone, payment, unit)
	if err != nil {
		log.Fatal("failed to send tenant SMS:", err)
	}

	log.Println("Tenant payment SMS sent successfully")

	// Optional: send reminder
	err = sms.SendReminder(context.Background(), payment.TenantPhone, unit.TenantName, unit.UnitRef, payment.Amount, "March")
	if err != nil {
		log.Fatal("failed to send reminder SMS:", err)
	}

	log.Println("Tenant reminder SMS sent successfully")
}
