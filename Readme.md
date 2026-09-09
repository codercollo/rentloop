# RentLoop

**Automated rent collection for Kenyan landlords via M-Pesa and WhatsApp.**

Tenants pay rent through **M-Pesa Paybill**. Landlords manage units, payments, arrears, reminders, and payment history directly through **WhatsApp** — no app required.

## Features

* M-Pesa Paybill payment tracking
* WhatsApp-based landlord management
* Automated rent reminders
* Monthly payment and arrears tracking
* Tenant payment history
* Deposit balance tracking
* Manual cash/bank payment recording
* Unmatched M-Pesa payment claiming
* Portfolio-level reporting

## Stack

**Go 1.22 · PostgreSQL 15 · Twilio · Safaricom Daraja · chi · pgx v5 · Caddy**

## Quick Start

### Prerequisites

* Go 1.22+
* PostgreSQL 15+
* ngrok
* Twilio Sandbox account
* Safaricom Daraja credentials

```bash
git clone https://github.com/codercollo/rentloop.git
cd rentloop

go mod download
cp .env.example .env

createdb rentloop
make migrate
make run
```

The server starts on `:8080`.

## Configuration

Copy `.env.example` to `.env` and configure the following:

| Variable                | Description                               |
| ----------------------- | ----------------------------------------- |
| `DATABASE_URL`          | PostgreSQL connection string              |
| `TWILIO_SID`            | Twilio Account SID                        |
| `TWILIO_TOKEN`          | Twilio Auth Token                         |
| `TWILIO_WHATSAPP_FROM`  | Twilio WhatsApp sender                    |
| `MPESA_CONSUMER_KEY`    | Daraja consumer key                       |
| `MPESA_CONSUMER_SECRET` | Daraja consumer secret                    |
| `MPESA_PAYBILL`         | M-Pesa Paybill shortcode                  |
| `MPESA_PASSKEY`         | Daraja STK passkey                        |
| `JWT_SECRET`            | JWT signing secret, minimum 32 characters |
| `APP_ENV`               | `development` or `production`             |

See [`.env.example`](.env.example) for the complete configuration.

## WhatsApp Commands

Landlords can manage their portfolio directly from WhatsApp:

| Command                             | Description                       |
| ----------------------------------- | --------------------------------- |
| `LIST`                              | View paid and unpaid tenants      |
| `TOTAL`                             | View collected vs expected rent   |
| `REMIND`                            | Remind all unpaid tenants         |
| `HISTORY A1`                        | View 3-month payment history      |
| `ANNUAL HISTORY A1`                 | View 12-month history and arrears |
| `LANDLORD-HISTORY`                  | View portfolio payment overview   |
| `DEPOSIT A1`                        | View deposit balance              |
| `MARK A1 PAID 12500 BANK`           | Record a manual payment           |
| `CLAIM TXN-123 TO A1`               | Assign an unmatched payment       |
| `ADD UNIT A1 John 0712345678 12500` | Add a rental unit                 |
| `HELP`                              | Show available commands           |

## Architecture

```text
Tenant
   │
   │ M-Pesa Payment
   ▼
Safaricom Daraja
   │
   ▼
RentLoop ─────── PostgreSQL
   │
   │ WhatsApp
   ▼
Landlord
```

RentLoop receives M-Pesa payment notifications, records them against rental units, and gives landlords real-time visibility through WhatsApp.

## Deployment

See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) for production deployment instructions.

## License

MIT © 2026 RentLoop
