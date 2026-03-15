# RentLoop

> Automated rent collection and management for Kenyan landlords — powered by M-Pesa and WhatsApp.

Landlords receive real-time WhatsApp notifications when tenants pay via M-Pesa. Tenants get instant SMS receipts. No app to install, no spreadsheet to update, no manual reconciliation.

---

## Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Getting Started](#getting-started)
- [Environment Variables](#environment-variables)
- [Database Migrations](#database-migrations)
- [Running Locally](#running-locally)
- [Testing](#testing)
- [Admin Panel](#admin-panel)
- [WhatsApp Bot Commands](#whatsapp-bot-commands)
- [Billing and Subscription](#billing-and-subscription)
- [Deployment](#deployment)
- [Project Structure](#project-structure)
- [Contributing](#contributing)

---

## Overview

**Problem:** Kenyan landlords with 5–50 rental units spend 2–4 hours each month manually cross-checking M-Pesa statements against tenant lists, then chasing unpaid tenants one by one.

**Solution:** RentLoop listens to M-Pesa Daraja C2B callbacks in real time. Every payment is automatically matched to a tenant by account reference, recorded in Postgres, and surfaced to the landlord via a WhatsApp bot — no login, no app, no spreadsheet.

**Stack:**

| Layer        | Technology                          |
| ------------ | ----------------------------------- |
| Backend      | Go 1.22                             |
| Database     | PostgreSQL 15 (pgx/v5)              |
| Router       | Chi v5                              |
| Admin UI     | Go html/template + HTMX             |
| Payments     | M-Pesa Daraja C2B/B2C               |
| Messaging    | Africa's Talking (WhatsApp + SMS)   |
| File storage | DigitalOcean Spaces (S3-compatible) |
| Auth         | JWT (HttpOnly cookie) + bcrypt      |
| Scheduler    | robfig/cron v3                      |
| CI/CD        | GitHub Actions                      |
| Hosting      | DigitalOcean Droplet + Nginx        |

---

## Architecture

RentLoop uses a strict three-layer architecture:

```
HTTP Request → Handler → Service → Repository → PostgreSQL
```

- **Handler** (`internal/*/handler.go`) — parse request, validate input, call service, write response. No business logic.
- **Service** (`internal/*/service.go`) — all business logic. Depends on interfaces, not concrete types. This is what unit tests cover.
- **Repository** (`internal/*/repository.go`) — SQL only. No logic. Returns domain types.
- **Domain** (`internal/domain/`) — shared structs (`Landlord`, `Unit`, `Payment`, `SubscriptionStatus`). No imports from other internal packages.

Services accept repository interfaces, so unit tests pass mocks without a real database.

**Payment flow:**

```
Tenant pays M-Pesa → Daraja fires POST /mpesa/c2b/callback
  → validator.go checks Safaricom IP + payload
  → goroutine: matcher → ledger → notifier
    → landlord gets WhatsApp message
    → tenant gets SMS receipt link
  → 200 OK returned immediately (before processing)
```

**Subscription payment flow:**

```
Landlord pays RENTOS-{landlord_id} → Daraja fires POST /billing/callback
  → billing.Service matches ref to landlord
  → sets subscription_status = active
  → sets billing_cycle_end = today + 30 days
  → records row in subscription_payments
  → WhatsApp: "Subscription renewed. Active until [date]."
```

---

## Prerequisites

- Go 1.22+
- PostgreSQL 15+
- A [Safaricom Daraja](https://developer.safaricom.co.ke) account with C2B enabled
- An [Africa's Talking](https://africastalking.com) account with WhatsApp and SMS
- A DigitalOcean Spaces bucket (or any S3-compatible store)
- An HTTPS domain — Daraja requires it for the callback URL

---

## Getting Started

```bash
git clone https://github.com/yourhandle/rentloop.git
cd rentloop
cp .env.example .env
# fill in .env (see Environment Variables below)
make migrate
make run
```

---

## Environment Variables

Copy `.env.example` to `.env` and populate all values. The application will not start if required variables are missing.

```bash
# App
PORT=8080
APP_ENV=development           # development | production

# Database
DATABASE_URL=postgres://user:pass@localhost:5432/rentloop?sslmode=disable

# M-Pesa Daraja
MPESA_ENV=sandbox             # sandbox | production
MPESA_CONSUMER_KEY=
MPESA_CONSUMER_SECRET=
MPESA_PAYBILL=
MPESA_PASSKEY=

# Africa's Talking
AT_API_KEY=
AT_USERNAME=
AT_WHATSAPP_NUMBER=           # e.g. +254700000000
AT_SMS_SENDER_ID=             # approved sender ID

# Admin Auth
JWT_SECRET=                   # min 32 chars, random
ACTIVATION_SECRET=            # for HMAC email tokens

# DigitalOcean Spaces
DO_SPACES_KEY=
DO_SPACES_SECRET=
DO_SPACES_BUCKET=
DO_SPACES_REGION=             # e.g. fra1
DO_SPACES_ENDPOINT=           # e.g. https://fra1.digitaloceanspaces.com

# Billing
BILLING_GRACE_DAYS=5          # days after expiry before suspension
SUBSCRIPTION_PRICE_PER_UNIT=50  # KES per unit per month
FREE_TIER_UNIT_LIMIT=10
```

> **Never commit `.env`.** It is listed in `.gitignore`. On the production droplet, store secrets in `/etc/rentloop.env`, readable only by the `rentloop` service user.

---

## Database Migrations

Migrations are plain SQL files in `internal/db/migrations/`, run in filename order.

```bash
# run all pending migrations
make migrate

# or directly
go run ./cmd/migrate
```

Migration files:

| File                  | Purpose                                                                                               |
| --------------------- | ----------------------------------------------------------------------------------------------------- |
| `001_init.sql`        | `landlords`, `units`, `payments` tables                                                               |
| `002_receipts.sql`    | `receipt_url`, `month_key` columns on payments                                                        |
| `003_admin_users.sql` | `admin_users` table                                                                                   |
| `004_billing.sql`     | `subscription_status`, `billing_cycle_end`, `unit_count` on landlords + `subscription_payments` table |

---

## Running Locally

```bash
# run with live reload (requires air)
make dev

# build binary
make build

# run binary directly
./bin/rentloop

# run with race detector
make run-race
```

The server starts on `http://localhost:8080`.

For local M-Pesa testing, expose your server with [ngrok](https://ngrok.com) and register the HTTPS URL in the Daraja sandbox portal:

```bash
ngrok http 8080
# register: https://abc123.ngrok.io/mpesa/c2b/callback
```

---

## Testing

```bash
# run all tests
make test

# with race detector
make test-race

# with coverage report
make test-cover

# single package
go test ./internal/ledger/... -v
```

Tests use mocks via interfaces — no test database required. The CI pipeline runs `go test ./... -race -count=1` on every push.

**Test coverage targets:**

| Package               | What is tested                                                                                                                   |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `internal/auth`       | bcrypt round-trip, JWT claims, activation token expiry                                                                           |
| `internal/ledger`     | full pay, partial pay, overpay, duplicate TransactionID idempotency                                                              |
| `internal/matcher`    | `"4b"`, `"4 B"`, `"unit4B"` all resolve to unit `4B`; unknown ref returns error                                                  |
| `internal/onboarding` | CSV validation: bad phone, missing rent, blank unit, duplicates                                                                  |
| `internal/mpesa`      | valid Safaricom IP accepted, unknown IP returns 403, malformed payload returns 400                                               |
| `internal/bot`        | command parsing: `LIST`, `REMIND`, `RECEIPT 4B`, unknown command returns help; suspended account returns lockout message         |
| `internal/billing`    | active→grace→suspended transitions, payment reactivates account, free tier never suspended, `unit_count × 50` amount calculation |

---

## Admin Panel

The admin panel is a server-rendered UI (Go `html/template` + HTMX) accessible at `/admin`.

| Route                           | Description                                                                        |
| ------------------------------- | ---------------------------------------------------------------------------------- |
| `GET /admin/login`              | Login form                                                                         |
| `POST /admin/login`             | Authenticate, set HttpOnly JWT cookie                                              |
| `GET /admin/register`           | Register new admin user                                                            |
| `POST /admin/register`          | Create user (activated=false), send activation email                               |
| `GET /admin/activate?token=`    | Verify token, set activated=true                                                   |
| `GET /admin/dashboard`          | MRR, active landlords, payments today, accounts in grace, suspended accounts       |
| `GET /admin/clients`            | All landlords — name, units, MRR, plan, subscription status, joined date           |
| `GET /admin/clients/:id`        | One landlord: units, tenant list, payment history, billing history, current status |
| `GET /admin/payments`           | All C2B transactions, filterable by date / landlord / status                       |
| `GET /admin/payments/unmatched` | Payments with no matching unit — assign or mark refunded                           |
| `POST /admin/logout`            | Clear JWT cookie                                                                   |

All `/admin/*` routes except login and register require a valid JWT via the `RequireAdmin` middleware.

---

## WhatsApp Bot Commands

Landlords interact with RentLoop entirely through WhatsApp. No app, no URL.

> All commands check `subscription_status` first via `billing.CheckSubscription`. Suspended accounts receive a single lockout message with payment instructions instead of the command response.

| Command                                   | Description                                      |
| ----------------------------------------- | ------------------------------------------------ |
| `JOIN`                                    | Start onboarding, receive CSV template           |
| `BULK ADD`                                | Re-send CSV template for bulk tenant upload      |
| `ADD UNIT 4B John Kamau 0712345678 12500` | Add a single unit inline                         |
| `LIST`                                    | Paid vs unpaid units for current month           |
| `REMIND`                                  | SMS all unpaid tenants a payment reminder        |
| `RECEIPT 4B`                              | Resend receipt for a specific unit               |
| `HISTORY 4B`                              | Last 3 months of payments for a unit             |
| `TOTAL`                                   | Total collected vs expected this month           |
| `REPLACE 4B Grace Auma 0745678901 12500`  | Swap tenant on a unit (soft-deletes previous)    |
| `SET RENT 4B 14000`                       | Update expected rent for a unit                  |
| `MARK 4B PAID 12500 BANK`                 | Manually log a bank or cash payment              |
| `CLAIM TXN-ABC123 TO 4B`                  | Assign an unmatched M-Pesa transaction to a unit |

A 6 PM daily digest is sent automatically to any landlord with unpaid units (active and grace accounts only).

---

## Billing and Subscription

### Tiers

| Tier   | Units        | Price                 |
| ------ | ------------ | --------------------- |
| Msingi | Up to 10     | Free forever          |
| Mjengo | 11 and above | KES 50 / unit / month |

### Subscription states

| State       | Behaviour                                                                                                          |
| ----------- | ------------------------------------------------------------------------------------------------------------------ |
| `active`    | Everything works normally                                                                                          |
| `grace`     | Full functionality, but every bot response appends a payment warning. Lasts `BILLING_GRACE_DAYS` days (default: 5) |
| `suspended` | All bot commands return a lockout message with payment instructions. C2B payments still recorded — no data is lost |

Free tier (`unit_count ≤ FREE_TIER_UNIT_LIMIT`) is permanently `active`. Billing logic never runs against it.

### Billing cycle

```
1st of month → cron job
  → landlords on paid tier with expired billing_cycle_end → status = grace
  → WhatsApp: "Subscription due: KES {amount}. Pay to Paybill {paybill}, ref: RENTLOOP-{id}"

Day 6 → cron job
  → landlords still in grace → status = suspended
  → WhatsApp: "Account suspended. Pay KES {amount} to reactivate."

On subscription payment (ref matches RENTLOOP-{landlord_id})
  → status = active
  → billing_cycle_end = today + 30 days
  → WhatsApp: "Subscription renewed. Active until {date}."
```

### Subscription amount

Calculated at billing time as `unit_count × KES 50`. Updates automatically when units are added. No proration in v1.

### How the gate works

`billing.CheckSubscription(landlordID)` is called at the top of every bot command handler and before every outbound notification. It is a single function — the gate lives in one place, not scattered across packages.

```go
// internal/billing/middleware.go
func (s *Service) CheckSubscription(ctx context.Context, landlordID string) error {
    landlord, _ := s.repo.GetByID(ctx, landlordID)
    if landlord.SubscriptionStatus == domain.StatusSuspended {
        return domain.ErrAccountSuspended
    }
    return nil
}
```

C2B payment recording (`ledger`) bypasses this check intentionally — incoming payment data is never blocked regardless of subscription status.

### Billing schema (`004_billing.sql`)

```sql
-- columns added to landlords
ALTER TABLE landlords
  ADD COLUMN subscription_status TEXT NOT NULL DEFAULT 'active',
  ADD COLUMN billing_cycle_end   DATE,
  ADD COLUMN unit_count          INTEGER NOT NULL DEFAULT 0;

-- full billing audit trail
CREATE TABLE subscription_payments (
  id              UUID PRIMARY KEY,
  landlord_id     UUID REFERENCES landlords(id),
  transaction_id  TEXT UNIQUE NOT NULL,
  amount          INTEGER NOT NULL,
  paid_at         TIMESTAMPTZ NOT NULL,
  period_start    DATE NOT NULL,
  period_end      DATE NOT NULL
);
```

---

## Deployment

### First-time server setup

```bash
# on your DigitalOcean droplet (Ubuntu 22.04)
adduser --system --group rentloop
mkdir -p /opt/rentloop /etc/rentloop
chown rentloop:rentloop /opt/rentloop

# install Postgres, Nginx, Certbot
apt install postgresql nginx certbot python3-certbot-nginx

# copy systemd service
cp deployments/rentloop.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable rentloop

# copy Nginx config and obtain SSL cert
cp deployments/nginx.conf /etc/nginx/sites-available/rentloop
ln -s /etc/nginx/sites-available/rentloop /etc/nginx/sites-enabled/
certbot --nginx -d api.yourdomain.co.ke
```

### Manual deploy

```bash
make deploy
# builds binary locally, scp to droplet, restarts systemd service
```

### Automated deploy via GitHub Actions

Push to `main` triggers `.github/workflows/deploy.yml`:

1. `test.yml` must pass first (`needs: test`)
2. Builds Linux binary (`GOOS=linux GOARCH=amd64`)
3. `scp` binary to droplet
4. SSH: `systemctl restart rentloop`
5. Health check: `curl https://api.yourdomain.co.ke/health`

**Required GitHub repository secrets:**

| Secret       | Value                                       |
| ------------ | ------------------------------------------- |
| `DO_HOST`    | Droplet IP address                          |
| `DO_USER`    | `rentloop` (deploy user)                    |
| `DO_SSH_KEY` | Private half of a dedicated ed25519 keypair |

---

## Project Structure

```
rentloop/
├── cmd/server/main.go            # entrypoint — wires all layers
├── internal/
│   ├── domain/                   # shared types, no business logic
│   ├── auth/                     # admin register / login / activate (JWT)
│   ├── admin/                    # dashboard, clients, payments views
│   ├── mpesa/                    # Daraja C2B webhook handler
│   ├── landlord/                 # landlord CRUD
│   ├── billing/                  # subscription lifecycle + lockout gate
│   ├── matcher/                  # AccountRef → tenant resolution
│   ├── ledger/                   # payment recording and status logic
│   ├── bot/                      # WhatsApp command parser + digest cron
│   ├── notifier/                 # WhatsApp + SMS outbound
│   ├── onboarding/               # JOIN flow + CSV bulk upload parser
│   ├── receipt/                  # PDF generation + Spaces upload
│   ├── config/                   # env loading
│   └── db/                       # pgx pool + SQL migrations
├── web/
│   ├── templates/                # Go html/template admin pages
│   └── static/                   # CSS, htmx.min.js
├── pkg/httputil/                 # shared JSON response helpers
├── deployments/                  # nginx.conf, systemd service file
├── .github/workflows/
│   ├── test.yml                  # on every push: go test ./... -race
│   └── deploy.yml                # on push to main: build + deploy
├── Makefile
├── .env.example
├── go.mod
└── go.sum
```

---

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/your-feature`
3. Write tests for any new service logic
4. Ensure `make test-race` passes with no failures
5. Open a pull request against `main`

All pull requests must pass the `test.yml` CI check before review.

---

## License

MIT — see [LICENSE](LICENSE) for details.
