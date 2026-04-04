# RentLoop

Automated rent collection for Kenyan landlords via M-Pesa and WhatsApp.

Tenants pay via M-Pesa Paybill. Landlords manage everything through WhatsApp commands — no app required.

---

## Stack

Go 1.22 · PostgreSQL 15 · Twilio (WhatsApp + SMS) · Safaricom Daraja C2B · chi · pgx v5 · Caddy

---

## Quick Start

**Prerequisites:** Go 1.22+, PostgreSQL 15+, ngrok, Twilio sandbox account

```bash
git clone https://github.com/codercollo/rentloop.git
cd rentloop
go mod download
cp .env.example .env        # fill in DATABASE_URL, TWILIO_SID, TWILIO_TOKEN
createdb rentloop
make migrate
make run                    # :8080
```

---

## Environment Variables

See [`.env.example`](.env.example) for the full list.

| Variable                | Description                   |
| ----------------------- | ----------------------------- |
| `DATABASE_URL`          | PostgreSQL DSN                |
| `TWILIO_SID`            | Twilio Account SID            |
| `TWILIO_TOKEN`          | Twilio Auth Token             |
| `TWILIO_WHATSAPP_FROM`  | e.g. `whatsapp:+14155238886`  |
| `MPESA_CONSUMER_KEY`    | Daraja API key                |
| `MPESA_CONSUMER_SECRET` | Daraja API secret             |
| `MPESA_PAYBILL`         | M-Pesa paybill shortcode      |
| `MPESA_PASSKEY`         | Daraja STK passkey            |
| `JWT_SECRET`            | Min 32 chars                  |
| `APP_ENV`               | `development` or `production` |

---

## Bot Commands

| Command                             | Description                   |
| ----------------------------------- | ----------------------------- |
| `LIST`                              | Paid vs unpaid this month     |
| `TOTAL`                             | Collected vs expected         |
| `REMIND`                            | Remind all unpaid tenants     |
| `HISTORY A1`                        | Last 3 months for a unit      |
| `ANNUAL HISTORY A1`                 | 12-month history with arrears |
| `LANDLORD-HISTORY`                  | 12-month portfolio overview   |
| `DEPOSIT A1`                        | Deposit balance for a unit    |
| `MARK A1 PAID 12500 BANK`           | Log a cash or bank payment    |
| `CLAIM TXN-123 TO A1`               | Assign an unmatched payment   |
| `ADD UNIT A1 John 0712345678 12500` | Add a unit                    |
| `HELP`                              | Show all commands             |

---

## Deployment

See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md).

---

## License

[MIT](LICENSE) © 2026 RentLoop
