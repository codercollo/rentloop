package main

import (
	"context"
	"log"
	"os"

	"github.com/codercollo/rentloop/internal/notifier"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file (adjusted path)
	if err := godotenv.Load("../.env"); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	apiKey := os.Getenv("AT_API_KEY")
	username := os.Getenv("AT_USERNAME")
	from := os.Getenv("AT_WHATSAPP_NUMBER")

	if apiKey == "" || username == "" || from == "" {
		log.Fatal("missing required environment variables: AT_API_KEY, AT_USERNAME, AT_WHATSAPP_NUMBER")
	}

	wa := notifier.NewWhatsApp(apiKey, username, from)

	err := wa.SendRaw(context.Background(), from, "Sandbox Activation Test via WhatsApp")
	if err != nil {
		log.Fatal("failed to send WhatsApp:", err)
	}

	log.Println("WhatsApp message sent — sandbox is now active")
}
