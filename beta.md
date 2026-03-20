# RentLoop — v1 Beta

> Automated rent collection for Kenyan landlords and property agents.
> Powered by M-Pesa and WhatsApp. No app. No spreadsheet. No manual work.

---

## Status: Beta — Pilot Ready

RentLoop v1 Beta is a working system available for a limited pilot with
landlords and property agents in Kenya. It is not a prototype — it is a
production backend connected to live M-Pesa Daraja and Africa's Talking APIs.

This document covers what the beta includes, what the pilot looks like,
and how to get access.

---

## The Problem

Every landlord with more than 5 units does the same thing on the 1st of
the month:

1. Opens their M-Pesa statement
2. Reads through every transaction
3. Matches each payment to a tenant name by hand
4. Writes down who has and has not paid
5. Calls or messages each unpaid tenant individually

For a landlord with 20 units this takes 2–4 hours. For a property agent
managing 5 landlords it can take an entire day. Every single month.

---

## What RentLoop Does

The moment a tenant pays rent via M-Pesa, RentLoop:

- Matches the payment to the correct tenant automatically
- Records it with paid / partial / unpaid status
- Sends the landlord or agent a WhatsApp notification instantly
- Sends the tenant an SMS receipt

The landlord does nothing. The payment is confirmed, recorded, and
receipted before they even pick up their phone.

At the end of the day, if any units are still unpaid, the landlord types
one word — `REMIND` — and every unpaid tenant gets an SMS reminder.

That is the entire workflow. No app to install. No URL to remember.
No spreadsheet. Just WhatsApp messages they were already going to receive.

---

## Beta Features — What Works Today

### Payment matching

Every M-Pesa payment to the configured Paybill is caught in real time.
The account reference (unit number) is normalised and matched to the
correct tenant automatically. Variations like `4b`, `4 B`, `UNIT 4B`,
and `unit-4B` all resolve to the same unit.

### Instant WhatsApp notification to landlord

```
RentLoop ✓
John Kamau (Unit 4B) paid KES 12,500
Status: Paid in full
Time: 01 Mar 08:14
Receipt: #a1b2c3d4
```

### Instant SMS receipt to tenant

```
RentLoop: KES 12,500 received for Unit 4B on 01 Mar 08:14.
Rent paid in full. Receipt: #a1b2c3d4.
```

### Payment status tracking

- **Paid** — full amount received
- **Partial** — some amount received, balance outstanding
- **Overpaid** — amount exceeds expected rent, flagged for review
- **Unmatched** — payment received but account reference not recognised,
  landlord alerted to claim it manually

### WhatsApp bot commands

All management happens in WhatsApp — no login, no URL.

| Command                   | What it does                                       |
| ------------------------- | -------------------------------------------------- |
| `LIST`                    | Shows paid vs unpaid units for the current month   |
| `REMIND`                  | Sends an SMS reminder to every unpaid tenant       |
| `RECEIPT 4B`              | Resends the receipt for a specific unit            |
| `TOTAL`                   | Shows total collected vs total expected this month |
| `HISTORY 4B`              | Last 3 months of payments for a unit               |
| `MARK 4B PAID 12500 BANK` | Manually log a bank or cash payment                |
| `CLAIM TXN-ABC123 TO 4B`  | Assign an unmatched transaction to a unit          |

### Tenant onboarding

Bulk upload all tenants in one step via CSV file sent in WhatsApp.
A 50-unit landlord is fully onboarded in under 20 minutes.

### 6 PM daily digest

If any units are unpaid at end of day, the landlord receives an automatic
summary — no command needed.

---

## Agent Support

Property agents who manage multiple landlords get a consolidated view
across their entire portfolio from a single WhatsApp number.

| Command          | What it does                                        |
| ---------------- | --------------------------------------------------- |
| `CLIENTS`        | Lists all managed landlords                         |
| `LIST Wanjiku`   | Paid vs unpaid for a specific landlord              |
| `REMIND Wanjiku` | Reminders for one landlord's unpaid tenants         |
| `TOTAL ALL`      | Combined collected vs expected across all landlords |

The agent's 1st of the month goes from a full day of manual work to
reviewing WhatsApp notifications and typing `REMIND Wanjiku`.

---

## What Is Not In Beta

The following are on the roadmap but not in the current beta:

- Web dashboard (Vue) — landlords manage everything via WhatsApp in v1
- Automated PDF receipts hosted online — receipts are SMS text in v1
- Multi-property support for very large portfolios
- QuickBooks / accounting software export

---

## Pilot Programme

The beta is being piloted with a small group of landlords and one
property agency before general release.

**What the pilot looks like:**

- Free for the duration of the pilot (4–8 weeks)
- You get the full product with real M-Pesa integration
- Setup takes under 30 minutes — we do it together
- You give honest feedback every time something is confusing or wrong
- If it saves you time on the 1st of the month, we ask for a testimonial

**What we ask from pilot participants:**

- At least 5 rental units on M-Pesa Paybill
- Willingness to use it for one full collection cycle (one month)
- 30 minutes for initial setup
- Honest feedback — good or bad

---

## Pricing After Pilot

| Tier       | Units        | Price                 |
| ---------- | ------------ | --------------------- |
| **Msingi** | Up to 10     | Free forever          |
| **Mjengo** | 11 and above | KES 50 / unit / month |

A landlord with 20 units pays KES 1,000 per month — less than the cost
of one hour spent on manual reconciliation.

**Agent pricing:**

| Units under management | Price                 |
| ---------------------- | --------------------- |
| Up to 10               | Free                  |
| 11–100 units           | KES 50 / unit / month |
| 101+ units             | KES 40 / unit / month |

An agent managing 5 landlords with 15 units each (75 units total) pays
KES 3,750/month and eliminates approximately 30 hours of manual work.

---

## Technical Foundation

RentLoop is not a WhatsApp chatbot bolted onto a spreadsheet.
It is a production Go backend with:

- Real-time M-Pesa Daraja C2B webhook integration
- PostgreSQL ledger with full payment audit trail
- Idempotent payment processing — duplicate M-Pesa callbacks are
  silently discarded, no double-counting ever
- Africa's Talking WhatsApp and SMS API integration
- Layered architecture with full unit test coverage
- Automated deployment via GitHub Actions to DigitalOcean

The system processes payments in under 3 seconds from M-Pesa confirmation
to WhatsApp notification.

---

## Get Access

To join the pilot or get notified when RentLoop opens to the public:

**WhatsApp:** +254 [your number]
**Email:** [your email]

Or message the word `JOIN` on WhatsApp to get started immediately.

---

_RentLoop is built in Kenya, for Kenya._
_Every design decision is based on how Kenyan landlords and tenants
actually use M-Pesa today — not how a Western product assumes they do._
