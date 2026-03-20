# RentLoop

> Automated rent collection and management for Kenyan landlords and property agents — powered by M-Pesa and WhatsApp.

Landlords and agents receive real-time WhatsApp notifications the moment a tenant pays via M-Pesa. Tenants get instant SMS receipts. No app to install, no spreadsheet to update, no manual reconciliation.

---

## Contents

- [What It Is](#what-it-is)
- [The Problem It Solves](#the-problem-it-solves)
- [Who It Is For](#who-it-is-for)
- [How It Works](#how-it-works)
- [System Architecture](#system-architecture)
- [Agent Model](#agent-model)
- [Pricing Tiers](#pricing-tiers)
- [WhatsApp Bot Commands](#whatsapp-bot-commands)
- [Subscription and Billing](#subscription-and-billing)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [Prerequisites](#prerequisites)
- [Getting Started](#getting-started)
- [Environment Variables](#environment-variables)
- [Database Migrations](#database-migrations)
- [Running Locally](#running-locally)
- [Testing](#testing)
- [Admin Panel](#admin-panel)
- [Deployment](#deployment)
- [Contributing](#contributing)

---

## What It Is

RentLoop is a backend payment system with three interfaces:

| Interface            | Who uses it      | Purpose                                   |
| -------------------- | ---------------- | ----------------------------------------- |
| WhatsApp bot         | Landlord / Agent | Daily management via chat commands        |
| Admin panel          | System operator  | Client management, payments, MRR, billing |
| Vue dashboard _(v2)_ | Landlord / Agent | Rich UI with charts, history, exports     |

The WhatsApp bot is the primary landlord interface in v1 — landlords already have WhatsApp open all day, no new app required. All three interfaces sit on top of the same Postgres database and payment engine.

---

## The Problem It Solves

Landlords with 5–50 units spend 2–4 hours every month doing four things manually:

1. Downloading and reading M-Pesa statements
2. Matching each payment to a tenant by name
3. Tracking which units have not paid
4. Sending individual reminder messages to unpaid tenants

Property agents who manage portfolios of multiple landlords multiply this problem — they do it for every landlord simultaneously, in notebooks and WhatsApp threads.

RentLoop eliminates all four. The 1st of the month becomes: receive notifications automatically, type `REMIND` once, done.

---

## Who It Is For

**Landlords** with 5–50 residential units who collect rent via M-Pesa and currently track payments manually.

**Property agents** who manage units on behalf of multiple landlords and need a consolidated view across their entire portfolio.

---

## How It Works

**Payment flow:**

```
Tenant pays to Paybill, account ref = unit (e.g. "4B")
  → M-Pesa fires POST /mpesa/c2b/callback within 3 seconds
  → RentLoop validates IP + payload, returns 200 immediately
  → goroutine: normalise ref → match to tenant → record in ledger
  → landlord receives WhatsApp notification
  → tenant receives SMS receipt
```

**Landlord side — entire month managed via WhatsApp:**

```
[08:02] RentLoop: John Kamau (Unit 4B) paid KES 12,500 ✓
[09:15] RentLoop: Mary Wanjiku (Unit 2A) paid KES 8,000 ✓
[18:00] RentLoop: 8 of 12 units paid. KES 94,500 collected.
        Unpaid: Peter Otieno 1C, Grace Auma 3A...
        Reply REMIND to send nudges.

Landlord: REMIND
RentLoop: Sent reminders to 4 unpaid tenants.
```

---

## System Architecture

```
HTTP Request → Handler → Service → Repository → PostgreSQL
```

- **Handler** — validate request, call service, write response. No business logic.
- **Service** — all business logic. Depends on interfaces. Unit-tested without a database.
- **Repository** — SQL only. No logic. Returns domain types.
- **Models** (`internal/models/`) — shared structs, no imports from other internal packages.

**Payment processing is asynchronous:**

```
POST /mpesa/c2b/callback
  → validate IP + payload
  → return 200 to Safaricom immediately      ← must respond within 5s or Daraja retries
  → goroutine: matcher → ledger → notifier
```

Idempotency is enforced by the `transaction_id UNIQUE` constraint — duplicate Daraja callbacks are silently discarded at the database level without any application logic.

---

## Agent Model

RentLoop supports two roles:

| Role         | Description                                                                            |
| ------------ | -------------------------------------------------------------------------------------- |
| **Landlord** | Manages their own units. Default role. `agent_id` is null.                             |
| **Agent**    | Manages multiple landlords on their behalf. Consolidated view across entire portfolio. |

```sql
CREATE TABLE agents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    whatsapp_phone TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE landlords ADD COLUMN agent_id UUID REFERENCES agents(id);
```

The bot detects the sender's role automatically from their WhatsApp number. An agent sees scoped multi-landlord commands. A landlord sees only their own units.

**Agent billing:** one consolidated subscription — `SUM(unit_count) × KES 50` across all managed landlords. Landlords under an agent do not pay separately.

---

## Pricing Tiers

| Tier       | Units        | Price                 |
| ---------- | ------------ | --------------------- |
| **Msingi** | Up to 10     | Free forever          |
| **Mjengo** | 11 and above | KES 50 / unit / month |

The free tier removes all signup friction. A landlord with 8 units gets the full product at zero cost and becomes a referral source. The paid tier activates automatically when unit count crosses 10.

---

## WhatsApp Bot Commands

All commands check `subscription_status` first. Suspended accounts receive a lockout message with payment instructions. C2B payment recording always proceeds regardless of subscription status — no payment data is ever blocked.

**Landlord commands:**

| Command                                   | Description                                 |
| ----------------------------------------- | ------------------------------------------- |
| `JOIN`                                    | Start onboarding, receive CSV template      |
| `BULK ADD`                                | Re-send CSV template for bulk tenant upload |
| `ADD UNIT 4B John Kamau 0712345678 12500` | Add a single unit inline                    |
| `LIST`                                    | Paid vs unpaid for current month            |
| `REMIND`                                  | SMS all unpaid tenants a reminder           |
| `RECEIPT 4B`                              | Resend receipt for a specific unit          |
| `HISTORY 4B`                              | Last 3 months of payments for a unit        |
| `TOTAL`                                   | Total collected vs expected this month      |
| `REPLACE 4B Grace Auma 0745678901 12500`  | Swap tenant on a unit                       |
| `SET RENT 4B 14000`                       | Update expected rent                        |
| `MARK 4B PAID 12500 BANK`                 | Log a bank or cash payment manually         |
| `CLAIM TXN-ABC123 TO 4B`                  | Assign an unmatched M-Pesa transaction      |

**Agent commands:**

| Command              | Description                                         |
| -------------------- | --------------------------------------------------- |
| `CLIENTS`            | List all managed landlords                          |
| `LIST Wanjiku`       | Paid vs unpaid for a specific landlord              |
| `REMIND Wanjiku`     | Send reminders for one landlord's unpaid tenants    |
| `RECEIPT Wanjiku 4B` | Resend receipt for a unit under a landlord          |
| `TOTAL ALL`          | Combined collected vs expected across all landlords |
| `TOTAL Wanjiku`      | Collected vs expected for one landlord              |

A 6 PM daily digest fires automatically for any landlord or agent with unpaid units.

---

## Subscription and Billing

| State       | Behaviour                                                                                       |
| ----------- | ----------------------------------------------------------------------------------------------- |
| `active`    | Full functionality                                                                              |
| `grace`     | Full functionality + warning on every bot response. Lasts `BILLING_GRACE_DAYS` days (default 5) |
| `suspended` | All bot commands return lockout message. Payments still recorded.                               |

Free tier is permanently `active` — billing logic never runs against it.

**Cycle:**

```
1st of month  → expired paid accounts → grace → WhatsApp due notice
Day 6         → grace accounts → suspended → WhatsApp suspension notice
On payment    → active, billing_cycle_end = today + 30 days → WhatsApp confirmation
```

Subscription payments use a dedicated account reference `RENTLOOP-{landlord_id}` on the same Paybill. The C2B webhook routes these to `billing.Service` instead of the rent ledger.

---

## Tech Stack

| Layer        | Technology                          |
| ------------ | ----------------------------------- |
| Backend      | Go 1.22                             |
| Database     | PostgreSQL 15 + pgx/v5              |
| Router       | Chi v5                              |
| Admin UI     | Go html/template + HTMX             |
| Payments     | M-Pesa Daraja C2B                   |
| Messaging    | Africa's Talking (WhatsApp + SMS)   |
| File storage | DigitalOcean Spaces (S3-compatible) |
| Auth         | JWT (HttpOnly cookie) + bcrypt      |
| Scheduler    | robfig/cron v3                      |
| CI/CD        | GitHub Actions                      |
| Hosting      | DigitalOcean Droplet + Nginx        |

---

## Project Structure

```
rentloop/
├── cmd/
│   ├── server/main.go           ← boot: config, DB, routes, cron, server
│   └── migrate/main.go          ← run migrations then exit
├── internal/
│   ├── models/                  ← shared domain types
│   │   ├── landlord.go
│   │   ├── unit.go
│   │   ├── payment.go
│   │   ├── subscription.go
│   │   ├── agent.go
│   │   └── errors.go
│   ├── mpesa/                   ← Daraja C2B webhook
│   ├── matcher/                 ← account ref → unit resolution
│   ├── ledger/                  ← payment recording + status logic
│   ├── notifier/                ← WhatsApp + SMS outbound
│   ├── bot/                     ← WhatsApp command parser
│   ├── onboarding/              ← JOIN flow + CSV bulk upload
│   ├── agent/                   ← agent management
│   ├── landlord/                ← landlord CRUD
│   ├── billing/                 ← subscription lifecycle + lockout gate
│   ├── auth/                    ← admin JWT auth
│   ├── admin/                   ← dashboard data
│   ├── receipt/                 ← PDF generation + Spaces upload
│   ├── config/config.go
│   └── db/
│       ├── db.go
│       └── migrations/
│           ├── 001_init.sql
│           ├── 002_receipts.sql
│           ├── 003_admin_users.sql
│           ├── 004_billing.sql
│           └── 005_agents.sql
├── web/
│   ├── templates/               ← admin HTML pages
│   └── static/                  ← admin.css, htmx.min.js
├── pkg/httputil/respond.go
├── deployments/                 ← nginx.conf, rentloop.service
├── .github/workflows/           ← test.yml, deploy.yml
├── .env.example
├── Makefile
├── go.mod
└── go.sum
```

---

## Prerequisites

- Go 1.22+
- PostgreSQL 15+
- [Safaricom Daraja](https://developer.safaricom.co.ke) account — sandbox or production
- [Africa's Talking](https://africastalking.com) account — WhatsApp + SMS
- DigitalOcean Spaces bucket — PDF receipt storage
- HTTPS domain — required by Daraja for the callback URL

---

## Getting Started

```bash
git clone https://github.com/yourhandle/rentloop.git
cd rentloop
cp .env.example .env
# fill in .env
make migrate
make run
# → http://localhost:8080/health → {"status":"ok"}
```

---

## Environment Variables

```bash
PORT=8080
APP_ENV=development

DATABASE_URL=postgres://rentloop:rentloop@localhost:5432/rentloop?sslmode=disable

MPESA_ENV=sandbox
MPESA_CONSUMER_KEY=
MPESA_CONSUMER_SECRET=
MPESA_PAYBILL=174379
MPESA_PASSKEY=

AT_API_KEY=
AT_USERNAME=sandbox
AT_WHATSAPP_NUMBER=+254700000000
AT_SMS_SENDER_ID=RentLoop

JWT_SECRET=                    # openssl rand -hex 32
ACTIVATION_SECRET=             # openssl rand -hex 32

DO_SPACES_KEY=
DO_SPACES_SECRET=
DO_SPACES_BUCKET=rentloop-receipts
DO_SPACES_REGION=fra1
DO_SPACES_ENDPOINT=https://fra1.digitaloceanspaces.com

BILLING_GRACE_DAYS=5
SUBSCRIPTION_PRICE_PER_UNIT=50
FREE_TIER_UNIT_LIMIT=10
```

Never commit `.env`. On the production droplet store at `/etc/rentloop.env`, `chmod 600`, readable only by the `rentloop` service user.

---

## Database Migrations

```bash
make migrate
```

| File                  | Creates                                                |
| --------------------- | ------------------------------------------------------ |
| `001_init.sql`        | `landlords`, `units`, `payments`                       |
| `002_receipts.sql`    | `receipts` table                                       |
| `003_admin_users.sql` | `admin_users`                                          |
| `004_billing.sql`     | billing columns on landlords + `subscription_payments` |
| `005_agents.sql`      | `agents` table + `agent_id` FK on landlords            |

---

## Running Locally

```bash
make run          # start server
make dev          # live reload (requires air)
make build        # compile → ./bin/rentloop
```

For M-Pesa sandbox testing, expose your server with ngrok:

```bash
ngrok http 8080
# register https://abc123.ngrok.io/mpesa/c2b/callback in Daraja portal
```

Simulate a test payment:

```bash
curl -X POST https://sandbox.safaricom.co.ke/mpesa/c2b/v1/simulate \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "ShortCode":     "174379",
    "CommandID":     "CustomerPayBillOnline",
    "Amount":        "12500",
    "Msisdn":        "254708374149",
    "BillRefNumber": "4B"
  }'
```

---

## Testing

```bash
make test          # all tests
make test-race     # with race detector
make test-cover    # coverage report → coverage.html
```

Tests use mocks via interfaces — no test database required.

| Package               | What is tested                                                            |
| --------------------- | ------------------------------------------------------------------------- |
| `internal/mpesa`      | Valid IP → 200, blocked IP → 403, bad JSON → 400, async response timing   |
| `internal/matcher`    | All ref variants, unknown ref → ErrNoMatch, landlord scoping              |
| `internal/ledger`     | Full, partial, closing balance, overpay, duplicate idempotency, unmatched |
| `internal/auth`       | bcrypt, JWT claims, activation token expiry                               |
| `internal/billing`    | active→grace→suspended, payment reactivates, free tier never suspended    |
| `internal/bot`        | Command parsing, suspended account lockout                                |
| `internal/onboarding` | CSV: bad phone, missing rent, blank unit, duplicates                      |

---

## Admin Panel

Accessible at `/admin`. All routes except login and register require a valid JWT.

| Route                            | Description                                                     |
| -------------------------------- | --------------------------------------------------------------- |
| `GET  /admin/login`              | Login form                                                      |
| `POST /admin/login`              | Authenticate, set HttpOnly JWT cookie                           |
| `GET  /admin/register`           | Register admin                                                  |
| `POST /admin/register`           | Create user, send activation email                              |
| `GET  /admin/activate?token=`    | Activate account                                                |
| `GET  /admin/dashboard`          | MRR, active landlords, payments today, grace + suspended counts |
| `GET  /admin/clients`            | All landlords — name, units, MRR, plan, status                  |
| `GET  /admin/clients/:id`        | One landlord: units, payment history, billing history           |
| `GET  /admin/agents`             | All agents — name, landlord count, total units, MRR             |
| `GET  /admin/agents/:id`         | One agent: all managed landlords, combined payment history      |
| `GET  /admin/payments`           | All C2B transactions, filterable by date/landlord/status        |
| `GET  /admin/payments/unmatched` | Unmatched payments — assign or mark refunded                    |
| `POST /admin/logout`             | Clear JWT cookie                                                |

---

## Deployment

**First-time setup (Ubuntu 22.04 droplet):**

```bash
adduser --system --group rentloop
mkdir -p /opt/rentloop
apt install postgresql nginx certbot python3-certbot-nginx
cp deployments/rentloop.service /etc/systemd/system/
systemctl daemon-reload && systemctl enable rentloop
cp deployments/nginx.conf /etc/nginx/sites-available/rentloop
ln -s /etc/nginx/sites-available/rentloop /etc/nginx/sites-enabled/
certbot --nginx -d api.yourdomain.co.ke
```

**Automated deploy (GitHub Actions):**

Push to `main` → tests pass → Linux binary built → scp to droplet → service restarted → health check.

Required secrets: `DO_HOST`, `DO_USER`, `DO_SSH_KEY`.

**Manual deploy:**

```bash
make deploy
```

---

## Contributing

1. Fork and create a feature branch: `git checkout -b feat/your-feature`
2. Write tests for any new service logic
3. `make test-race` must pass with no failures
4. Open a pull request against `main`

All PRs must pass `test.yml` before review.

---

## License

MIT — see [LICENSE](LICENSE) for details.
