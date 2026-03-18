# RentLoop — v1 Beta

> Automated rent collection for Kenyan landlords and property agents — powered by M-Pesa and WhatsApp.

Landlords receive real-time WhatsApp notifications the moment a tenant pays via M-Pesa. Tenants get instant SMS receipts. No app. No spreadsheet. No manual work.

---

## Contents

- [What v1 Beta Is](#what-v1-beta-is)
- [The Problem It Solves](#the-problem-it-solves)
- [Who It Is For](#who-it-is-for)
- [How It Works](#how-it-works)
- [Features in v1 Beta](#features-in-v1-beta)
- [What Is Not in v1 Beta](#what-is-not-in-v1-beta)
- [WhatsApp Bot Commands](#whatsapp-bot-commands)
- [Pricing](#pricing)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [Prerequisites](#prerequisites)
- [Getting Started](#getting-started)
- [Environment Variables](#environment-variables)
- [Database Migrations](#database-migrations)
- [Running Locally](#running-locally)
- [Testing](#testing)
- [Deployment](#deployment)
- [Contributing](#contributing)

---

## What v1 Beta Is

RentLoop v1 Beta is the first working version of the system — scoped deliberately to the core problem: **getting landlords off M-Pesa statements and WhatsApp voice notes into an automated payment loop**.

It is a Go backend connected to live M-Pesa Daraja and Africa's Talking APIs. The landlord interface is entirely WhatsApp. There is no web dashboard in v1 — that is v2.

**Build status:**

| Component                 | Status  |
| ------------------------- | ------- |
| M-Pesa C2B webhook        | ✓ Built |
| Payment matcher           | ✓ Built |
| Payment ledger            | ✓ Built |
| WhatsApp + SMS notifier   | ✓ Built |
| WhatsApp bot commands     | ✓ Built |
| Tenant CSV onboarding     | ✓ Built |
| Subscription billing gate | ✓ Built |
| Admin panel               | ✓ Built |
| Agent support             | ✓ Built |
| Vue dashboard             | ✗ v2    |
| PDF receipt hosting       | ✗ v2    |
| Accounting export         | ✗ v2    |

---

## The Problem It Solves

Landlords with 5–50 units spend 2–4 hours every month doing four things manually:

1. Downloading and reading M-Pesa statements
2. Matching each payment to a tenant by name
3. Tracking which units have not paid
4. Messaging each unpaid tenant individually

Property agents managing multiple landlords do this for every landlord simultaneously — in notebooks and WhatsApp threads.

RentLoop eliminates all four. The 1st of the month becomes: receive notifications automatically, type `REMIND` once, done.

---

## Who It Is For

**Landlords** with 5–50 residential units collecting rent via M-Pesa who currently reconcile payments manually.

**Property agents** managing units on behalf of multiple landlords who need a single consolidated view across their entire portfolio.

---

## How It Works

```
Tenant pays to Paybill, account ref = unit number (e.g. "4B")
  → M-Pesa fires POST /mpesa/c2b/callback within 3 seconds
  → RentLoop validates IP + payload, returns 200 immediately
  → goroutine: normalise ref → match tenant → record payment
  → landlord receives WhatsApp notification instantly
  → tenant receives SMS receipt
```

**What the landlord sees on the 1st of the month:**

```
[08:02] RentLoop: John Kamau (Unit 4B) paid KES 12,500 ✓
[09:15] RentLoop: Mary Wanjiku (Unit 2A) paid KES 8,000 ✓
[18:00] RentLoop: 8 of 12 units paid. KES 94,500 collected.
        Unpaid: Peter Otieno 1C, Grace Auma 3A...
        Reply REMIND to send nudges.

Landlord: REMIND
RentLoop: Sent reminders to 4 unpaid tenants.
```

No login. No URL. No app. Just WhatsApp.

---

## Features in v1 Beta

### Real-time payment matching

Every M-Pesa payment to the Paybill is caught instantly via Daraja C2B.
The account reference is normalised and matched to the correct tenant
automatically. `4b`, `4 B`, `UNIT 4B`, and `unit-4B` all resolve to
the same unit — tenants do not need to type the ref precisely.

### Payment status tracking

Every payment is classified automatically:

| Status      | Meaning                                      |
| ----------- | -------------------------------------------- |
| `paid`      | Full expected rent received                  |
| `partial`   | Some amount received, balance outstanding    |
| `overpaid`  | Amount exceeds expected rent                 |
| `unmatched` | Payment received, account ref not recognised |

Partial payments accumulate — a second payment closes the balance
and updates status to paid.

### Instant WhatsApp notification — landlord

Sent within 3 seconds of the M-Pesa confirmation:

```
RentLoop ✓
John Kamau (Unit 4B) paid KES 12,500
Status: Paid in full
Time: 01 Mar 08:14
Receipt: #a1b2c3d4
```

### Instant SMS receipt — tenant

```
RentLoop: KES 12,500 received for Unit 4B on 01 Mar 08:14.
Rent paid in full. Receipt: #a1b2c3d4.
```

### WhatsApp bot commands

Full management from WhatsApp — no login, no URL. See full command
list below.

### Bulk tenant onboarding via CSV

Landlord sends a filled CSV file in WhatsApp. Up to 50 units load
in under 20 minutes. Validation errors reported row by row.

### 6 PM automatic daily digest

If any units are unpaid at end of day, the landlord receives a summary
automatically — no command needed.

### Unmatched payment alerts

When a payment arrives with an unrecognised account reference, the
landlord is alerted immediately and can claim it manually with
`CLAIM TXN-ABC123 TO 4B`.

### Agent support

Property agents managing multiple landlords get a consolidated view
across their entire portfolio from a single WhatsApp number.
See agent commands below.

### Subscription billing

Free for up to 10 units. Paid tier activates automatically above 10
units. Accounts move through `active → grace → suspended` states.
Suspended accounts receive a lockout message — payment recording
continues regardless so no data is ever lost.

### Admin panel

Server-rendered dashboard for the system operator. Shows all landlords,
agents, payment history, unmatched transactions, MRR, and billing
status. Secured with JWT auth and email activation.

---

## What Is Not in v1 Beta

| Feature                                          | Planned for |
| ------------------------------------------------ | ----------- |
| Vue web dashboard for landlords                  | v2          |
| PDF receipts hosted online                       | v2          |
| STK Push — push payment prompt to tenant's phone | v2          |
| QuickBooks / accounting export                   | v2          |
| Multi-property support for 500+ unit portfolios  | v2          |
| Utility billing (water, electricity)             | v3          |
| Tenant credit score from payment history         | v3          |

---

## WhatsApp Bot Commands

All commands check subscription status first. Suspended accounts
receive a lockout message with payment instructions instead of the
command response. Payment recording always proceeds regardless.

**Landlord commands:**

| Command                                   | Description                            |
| ----------------------------------------- | -------------------------------------- |
| `JOIN`                                    | Start onboarding, receive CSV template |
| `BULK ADD`                                | Re-send CSV template                   |
| `ADD UNIT 4B John Kamau 0712345678 12500` | Add a single unit inline               |
| `LIST`                                    | Paid vs unpaid for current month       |
| `REMIND`                                  | SMS all unpaid tenants a reminder      |
| `RECEIPT 4B`                              | Resend receipt for a specific unit     |
| `HISTORY 4B`                              | Last 3 months of payments for a unit   |
| `TOTAL`                                   | Total collected vs expected this month |
| `REPLACE 4B Grace Auma 0745678901 12500`  | Swap tenant on a unit                  |
| `SET RENT 4B 14000`                       | Update expected rent                   |
| `MARK 4B PAID 12500 BANK`                 | Log a bank or cash payment manually    |
| `CLAIM TXN-ABC123 TO 4B`                  | Assign an unmatched transaction        |

**Agent commands:**

| Command              | Description                                         |
| -------------------- | --------------------------------------------------- |
| `CLIENTS`            | List all managed landlords                          |
| `LIST Wanjiku`       | Paid vs unpaid for a specific landlord              |
| `REMIND Wanjiku`     | Reminders for one landlord's unpaid tenants         |
| `RECEIPT Wanjiku 4B` | Resend receipt for a unit under a landlord          |
| `TOTAL ALL`          | Combined collected vs expected across all landlords |
| `TOTAL Wanjiku`      | Collected vs expected for one landlord              |

---

## Pricing

**Landlord tiers:**

| Tier       | Units        | Price                 |
| ---------- | ------------ | --------------------- |
| **Msingi** | Up to 10     | Free forever          |
| **Mjengo** | 11 and above | KES 50 / unit / month |

**Agent tiers:**

| Units under management | Price                 |
| ---------------------- | --------------------- |
| Up to 10               | Free                  |
| 11–100                 | KES 50 / unit / month |
| 101+                   | KES 40 / unit / month |

Subscription payments use account reference `RENTLOOP-{id}` on the
same Paybill. Renewal is automatic on payment.

---

## Tech Stack

| Layer        | Technology                        |
| ------------ | --------------------------------- |
| Backend      | Go 1.22                           |
| Database     | PostgreSQL 15 + pgx/v5            |
| Router       | Chi v5                            |
| Admin UI     | Go html/template + HTMX           |
| Payments     | M-Pesa Daraja C2B                 |
| Messaging    | Africa's Talking (WhatsApp + SMS) |
| File storage | DigitalOcean Spaces               |
| Auth         | JWT (HttpOnly cookie) + bcrypt    |
| Scheduler    | robfig/cron v3                    |
| CI/CD        | GitHub Actions                    |
| Hosting      | DigitalOcean Droplet + Nginx      |

---

## Project Structure

```
rentloop/
├── cmd/
│   ├── server/main.go           ← boot: config, DB, routes, cron, server
│   └── migrate/main.go          ← run migrations then exit
├── internal/
│   ├── models/                  ← landlord, unit, payment, subscription, agent, errors
│   ├── mpesa/                   ← Daraja C2B webhook + validator
│   ├── matcher/                 ← account ref → unit resolution
│   ├── ledger/                  ← payment recording + paid/partial/overpaid logic
│   ├── notifier/                ← WhatsApp + SMS outbound
│   ├── bot/                     ← landlord and agent command parser + digest cron
│   ├── onboarding/              ← JOIN flow + CSV bulk upload
│   ├── agent/                   ← agent management
│   ├── landlord/                ← landlord CRUD
│   ├── billing/                 ← subscription lifecycle + lockout gate
│   ├── auth/                    ← admin JWT auth
│   ├── admin/                   ← dashboard data + routes
│   ├── receipt/                 ← receipt generation + storage
│   ├── config/config.go         ← env loading + validation
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
- [Safaricom Daraja](https://developer.safaricom.co.ke) — C2B sandbox or production
- [Africa's Talking](https://africastalking.com) — WhatsApp + SMS
- DigitalOcean Spaces bucket — receipt storage
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

Never commit `.env`. On the production droplet store at `/etc/rentloop.env`,
`chmod 600`, readable only by the `rentloop` service user.

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
make test-cover    # coverage → coverage.html
```

Tests use interface mocks — no test database required.

| Package               | What is tested                                                  |
| --------------------- | --------------------------------------------------------------- |
| `internal/mpesa`      | Valid IP → 200, blocked IP → 403, bad JSON → 400, async timing  |
| `internal/matcher`    | All ref variants, ErrNoMatch, landlord scoping                  |
| `internal/ledger`     | Full, partial, closing balance, overpay, idempotency, unmatched |
| `internal/auth`       | bcrypt, JWT claims, activation token expiry                     |
| `internal/billing`    | active→grace→suspended transitions, free tier never suspended   |
| `internal/bot`        | Command parsing, suspended account lockout                      |
| `internal/onboarding` | CSV: bad phone, missing rent, blank unit, duplicates            |

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

**Automated (GitHub Actions):**

Push to `main` → tests pass → binary built → scp to droplet → restart → health check.

Required secrets: `DO_HOST`, `DO_USER`, `DO_SSH_KEY`.

**Manual:**

```bash
make deploy
```

---

## Contributing

1. Fork and create a branch: `git checkout -b feat/your-feature`
2. Write tests for any new service logic
3. `make test-race` must pass
4. Open a pull request against `main`

All PRs must pass `test.yml` before review.

---

## License

MIT — see [LICENSE](LICENSE) for details.
