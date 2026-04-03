# RentLoop — Architecture

## Overview

RentLoop is a single-binary Go service that bridges Safaricom M-Pesa Daraja
with WhatsApp (via Twilio) to automate rent collection for Kenyan landlords.

It is deliberately simple: one process, one PostgreSQL database, one VPS.
No microservices, no message queues, no Kubernetes. The system handles
hundreds of landlords and thousands of units comfortably on a $12/month
DigitalOcean droplet.

---

## System Diagram

```
┌──────────────────────────────────────────────────────────────────────┐
│                          Internet                                     │
│                                                                      │
│  Tenant (M-Pesa)          Landlord (WhatsApp)     Admin (Browser)   │
│       │                         │                       │            │
└───────┼─────────────────────────┼───────────────────────┼────────────┘
        │                         │                       │
        ▼                         ▼                       ▼
┌───────────────────────────────────────────────────────────────────┐
│                    Caddy (TLS termination)                         │
│                    api.rentloop.co.ke → :8080                      │
└───────────────────────────────────┬───────────────────────────────┘
                                    │
┌───────────────────────────────────▼───────────────────────────────┐
│                      RentLoop Go Server (:8080)                    │
│                                                                    │
│  ┌─────────────────┐  ┌──────────────────┐  ┌─────────────────┐  │
│  │  mpesa.Handler  │  │   bot.Handler    │  │  admin.Handler  │  │
│  │                 │  │                  │  │                 │  │
│  │ /mpesa/c2b/     │  │ /bot/whatsapp    │  │ /admin/*        │  │
│  │ /mpesa/stk/     │  │ /bot/sms         │  │                 │  │
│  └────────┬────────┘  └────────┬─────────┘  └────────┬────────┘  │
│           │                    │                      │            │
│  ┌────────▼────────────────────▼──────────────────────▼────────┐  │
│  │                      Core Services                          │  │
│  │                                                             │  │
│  │  ledger.Service   matcher.Service   receipt.Service         │  │
│  │  deposit.Service  billing.Service   notifier.Twilio         │  │
│  └────────────────────────────┬────────────────────────────────┘  │
│                               │                                    │
└───────────────────────────────┼────────────────────────────────────┘
                                │
            ┌───────────────────┼───────────────────┐
            │                   │                   │
            ▼                   ▼                   ▼
    ┌───────────────┐  ┌────────────────┐  ┌────────────────────┐
    │  PostgreSQL   │  │  Twilio API    │  │  DO Spaces (S3)    │
    │               │  │  (WhatsApp +   │  │  (PDF receipts)    │
    │  payments     │  │   SMS)         │  │                    │
    │  receipts     │  └────────────────┘  └────────────────────┘
    │  units        │
    │  landlords    │
    │  deposits     │
    └───────────────┘
```

---

## Request Lifecycle — M-Pesa Payment

```
1. Tenant pays KES 12,500 to Paybill 174379, Account: A1
        │
2. Safaricom POSTs callback to /mpesa/c2b/callback
        │
3. mpesa.Handler.Callback()
   ├── ValidateIP()              — allow Safaricom IPs only (or skip in dev)
   ├── json.Decode()             — parse C2BCallback struct
   ├── ValidatePayload()         — check required fields
   ├── writeJSON(200, Accepted)  — ACK Safaricom FIRST (< 5s required)
   └── go h.process()            — ALL heavy work in background goroutine
        │
4. process() goroutine
   ├── billing.IsSubscriptionPayment()  — skip if RENTLOOP- prefix
   ├── landlords.GetByPaybill()         — resolve landlord from paybill
   ├── matcher.Match()                  — normalise "A1" → unit row
   ├── ledger.Record()                  — insert payment, derive status
   ├── notifier.NotifyLandlord()        — WhatsApp: "KES 12,500 from A1"
   ├── notifier.NotifyTenant()          — WhatsApp: "Receipt #xyz"
   └── receiptSvc.IssueAsync()
            │
5. receipt goroutine
   ├── GetReceiptByPaymentID()   — idempotency check
   ├── generatePDF()             — gofpdf builds A4 PDF in memory
   ├── uploader.Upload()         — PUT to DO Spaces → public URL
   ├── InsertReceipt()           — write receipts row to DB
   └── onDone callback           — log receipt_number + URL
```

---

## Package Structure

```
rentloop/
├── cmd/
│   ├── server/          # main() — wires all packages, starts HTTP server
│   └── migrate/         # runs SQL migrations from internal/db/migrations/
│
├── internal/
│   ├── admin/           # Admin dashboard handlers + repository
│   ├── auth/            # JWT-based admin authentication
│   ├── billing/         # SaaS subscription billing (STK Push)
│   ├── bot/             # WhatsApp/SMS command parser and handler
│   ├── config/          # Environment variable loading + validation
│   ├── db/              # pgxpool connection factory
│   │   └── migrations/  # SQL migration files (numbered)
│   ├── deposit/         # Per-unit deposit ledger
│   ├── ledger/          # Payment recording + status derivation
│   ├── matcher/         # M-Pesa account ref → unit normalisation
│   ├── middleware/       # Rate limiting, CORS, Twilio validation, auth
│   ├── models/          # Domain structs (Payment, Unit, Landlord, Receipt…)
│   ├── mpesa/           # Daraja C2B + STK callback handlers
│   │   └── stk/         # STK Push client
│   ├── notifier/        # Twilio WhatsApp + SMS outbound messages
│   ├── onboarding/      # Landlord registration + CSV bulk upload
│   └── receipt/         # PDF generation + DO Spaces upload
│
├── web/
│   ├── static/          # CSS, JS, images
│   └── templates/       # HTML templates (admin dashboard)
│
├── docs/                # Deployment guide, generated specs
├── .env.example
├── docker-compose.yaml
├── Dockerfile
├── Makefile
├── openapi.yaml
└── README.md
```

---

## Data Model

```sql
landlords        -- one row per registered landlord
  id, whatsapp_phone, name, apartment_name, premise_name,
  paybill_number, subscription_status, unit_count

units            -- one row per rental unit
  id, landlord_id, unit_ref, tenant_name, tenant_phone,
  expected_rent, active, effective_from

payments         -- one row per M-Pesa transaction
  id, transaction_id (UNIQUE), unit_id, landlord_id,
  tenant_phone, amount, status, month_key,
  expected_rent_snapshot, arrears_carried,
  payment_source, paid_at

receipts         -- one row per issued PDF receipt
  id, payment_id (FK), landlord_id, unit_id,
  receipt_number (UNIQUE), storage_key, public_url,
  sent_to_phone, sent_at

deposits         -- current deposit state per unit
  id, unit_id (UNIQUE), landlord_id,
  deposit_expected, deposit_paid, deposit_balance, deposit_refunded

deposit_transactions  -- full audit trail for deposit mutations
  id, deposit_id, unit_id, txn_type, amount, note, recorded_by
```

---

## Key Design Decisions

### Idempotency everywhere

Safaricom retries its callback up to 3 times. Every write path is idempotent:

- `payments.transaction_id` has a `UNIQUE` constraint → duplicate M-Pesa
  transactions silently return `nil, nil`.
- `receipts.receipt_number` has a `UNIQUE` constraint + an early-return check
  in `Issue()` → duplicate receipt generation is a no-op.

### Async after ACK

Safaricom requires a `200 OK` with `ResultCode: 0` within 5 seconds or it
retries. The callback handler sends the ACK immediately and does all heavy
work (DB writes, Twilio calls, PDF generation) in a background goroutine.

### Uploader interface

`receipt.Uploader` is an interface. In `development` mode, `main.go` injects
a `fakeUploader` that writes PDFs to `tmp/receipts/` and serves them at
`/dev/receipts/*`. In `production`, the real `spacesUploader` uploads to
DigitalOcean Spaces. No code changes needed between environments.

### Single binary

The Go binary embeds all business logic. There is no separate worker process.
Background jobs (digest cron, billing cron) run as goroutines inside the same
process, scheduled via `robfig/cron`.

---

## Scalability Notes

The current architecture handles ~500 landlords / 10,000 units on a single
$12/month droplet with:

- PostgreSQL connection pool: 5–25 connections
- Goroutine-per-callback concurrency model
- No external cache required

When you exceed this:

1. Move PostgreSQL to a managed database (DO Managed Postgres — $15/mo).
2. Add a Redis cache for hot landlord lookups.
3. Replace the in-process rate limiter with Redis INCR + EXPIRE.
4. Consider splitting the receipt worker into a separate process reading
   from a task queue (e.g. Asynq).
