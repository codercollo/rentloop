# RentLoop — Automated Rent Collection for Kenyan Landlords

> **Beta** — production-ready core, actively maintained.

RentLoop connects M-Pesa Daraja (C2B paybill + STK Push) to a WhatsApp bot so
landlords collect rent, issue PDF receipts, and track arrears automatically —
no app download required for tenants.

---

## Table of Contents

- [What It Does](#what-it-does)
- [Tech Stack](#tech-stack)
- [Architecture](#architecture)
- [Quick Start (Local)](#quick-start-local)
- [Environment Variables](#environment-variables)
- [API Reference](#api-reference)
- [WhatsApp Bot Commands](#whatsapp-bot-commands)
- [Deployment](#deployment)
- [Contributing](#contributing)
- [License](#license)

---

## What It Does

| Feature | Details |
|---|---|
| **Automatic payment matching** | Tenant pays Paybill `174379`, account = unit ref (e.g. `A1`). System matches to landlord → unit instantly. |
| **PDF receipts** | Generated in <15 s, uploaded to DigitalOcean Spaces, URL sent to tenant via WhatsApp. |
| **WhatsApp bot** | Landlord sends commands (`LIST`, `TOTAL`, `REMIND`, etc.) from their phone. No dashboard needed. |
| **Arrears tracking** | Partial payments carry forward month-to-month. |
| **Deposit ledger** | Per-unit deposit tracking with full audit trail. |
| **Subscription billing** | SaaS billing via STK Push — KES 50/unit/month, free for first 3 units. |

---

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.22 |
| HTTP router | [chi](https://github.com/go-chi/chi) |
| Database | PostgreSQL 15 (pgx v5 driver) |
| WhatsApp / SMS | Twilio |
| M-Pesa | Safaricom Daraja C2B + STK Push |
| PDF generation | [gofpdf](https://github.com/jung-kurt/gofpdf) |
| File storage | DigitalOcean Spaces (S3-compatible) |
| Reverse proxy | Caddy |
| Hosting | DigitalOcean Droplet |
| CI/CD | GitHub Actions |

---

## Architecture

```
Tenant
  │  pays via M-Pesa
  ▼
Safaricom Daraja ──POST /mpesa/c2b/callback──▶ mpesa.Handler
                                                    │
                          ┌─────────────────────────┤
                          │                         │
                    ledger.Record()          receipt.IssueAsync()
                          │                         │
                    payments table           generatePDF()
                          │                    spacesUploader.Upload()
                    notifier.Notify*()         receipts table
                    (WhatsApp via Twilio)

Landlord
  │  sends WhatsApp command
  ▼
Twilio ──POST /bot/whatsapp──▶ bot.Handler ──▶ bot.Service ──▶ DB
                                                    │
                                             replies via Twilio
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full system design.

---

## Quick Start (Local)

### Prerequisites

- Go 1.22+
- PostgreSQL 15+
- [ngrok](https://ngrok.com) (free tier) for webhook testing
- A Twilio sandbox account

### 1. Clone and install

```bash
git clone https://github.com/codercollo/rentloop.git
cd rentloop
go mod download
```

### 2. Configure environment

```bash
cp .env.example .env
# Edit .env — minimum required: DATABASE_URL, TWILIO_SID, TWILIO_TOKEN
```

### 3. Create the database

```bash
createdb rentloop
make migrate
```

### 4. Run

```bash
make run
# Server starts on :8080
```

### 5. Expose via ngrok (for M-Pesa callbacks)

```bash
ngrok http 8080
# Copy the https URL into .env as MPESA_CALLBACK_URL
```

### 6. Test the receipt pipeline

```bash
curl -s -X POST "http://localhost:8080/mpesa/c2b/callback" \
  -H "Content-Type: application/json" \
  -d '{
    "TransactionType": "Pay Bill",
    "TransID":         "TEST-001",
    "TransTime":       "20260403120000",
    "TransAmount":     "12500.00",
    "BusinessShortCode": "174379",
    "BillRefNumber":   "A1",
    "MSISDN":          "254712345678",
    "FirstName":       "John",
    "LastName":        "Kamau"
  }'
```

In dev mode the PDF is saved locally and served at:
`http://localhost:8080/dev/receipts/receipts/{landlord-id}/{receipt-number}.pdf`

---

## Environment Variables

See [.env.example](.env.example) for the full list with descriptions.

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | ✅ | PostgreSQL DSN |
| `TWILIO_SID` | ✅ | Twilio Account SID |
| `TWILIO_TOKEN` | ✅ | Twilio Auth Token |
| `TWILIO_WHATSAPP_FROM` | ✅ | Sender number e.g. `whatsapp:+14155238886` |
| `MPESA_CONSUMER_KEY` | ✅ | Daraja API consumer key |
| `MPESA_CONSUMER_SECRET` | ✅ | Daraja API consumer secret |
| `MPESA_PAYBILL` | ✅ | Your M-Pesa paybill shortcode |
| `MPESA_PASSKEY` | ✅ | Daraja STK passkey |
| `DO_SPACES_KEY` | ✅ prod | DigitalOcean Spaces access key |
| `DO_SPACES_SECRET` | ✅ prod | DigitalOcean Spaces secret |
| `DO_SPACES_BUCKET` | ✅ prod | Spaces bucket name |
| `JWT_SECRET` | ✅ | Min 32 chars — admin dashboard auth |
| `APP_ENV` | — | `development` (default) or `production` |

---

## API Reference

Full OpenAPI 3.0 spec: [openapi.yaml](openapi.yaml)

| Method | Path | Description |
|---|---|---|
| `POST` | `/mpesa/c2b/callback` | Safaricom C2B payment callback |
| `POST` | `/mpesa/stk/callback` | STK Push result callback |
| `POST` | `/bot/whatsapp` | Twilio WhatsApp inbound webhook |
| `POST` | `/bot/sms` | Twilio SMS inbound webhook |
| `GET` | `/health` | Health check |
| `GET` | `/admin/dashboard` | Admin dashboard (JWT) |

---

## WhatsApp Bot Commands

Send any command from your registered WhatsApp number:

| Command | Description |
|---|---|
| `LIST` | Units paid vs unpaid this month |
| `TOTAL` | Collected vs expected summary |
| `REMIND` | SMS all unpaid tenants |
| `RECEIPT A1` | Resend receipt for unit A1 |
| `HISTORY A1` | Last 3 months for a unit |
| `HISTORY-EXT A1` | Full 12-month history with arrears |
| `LANDLORD-HISTORY` | 12-month portfolio performance |
| `DEPOSIT A1` | Deposit balance for a unit |
| `DEPOSIT-REFUND A1 15000` | Record a deposit refund |
| `MARK A1 PAID 12500 BANK` | Log a cash/bank payment |
| `CLAIM TXN-123 TO A1` | Assign an unmatched payment |
| `ADD UNIT A1 John 0712345678 12500` | Add a new unit |
| `JOIN` | Register as a new landlord |
| `HELP` | Show all commands |

---

## Deployment

See [DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full production setup guide including:

- DigitalOcean Droplet provisioning
- PostgreSQL setup
- Caddy reverse proxy configuration
- systemd service installation
- GitHub Actions CI/CD pipeline
- DigitalOcean Spaces bucket setup

**One-command deploy** (after initial setup):

```bash
git push origin main
# GitHub Actions runs tests → builds Linux binary → deploys → health checks
```

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## License

[MIT](LICENSE) © 2026 RentLoop
