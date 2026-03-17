## Overview

**Problem:** Kenyan landlords with 5–50 rental units spend 2–4 hours each month
manually cross-checking M-Pesa statements against tenant lists, then chasing
unpaid tenants one by one. Property managers and agents who manage multiple
landlords multiply this problem — they are doing this manually for every
landlord in their portfolio simultaneously.

**Solution:** RentLoop listens to M-Pesa Daraja C2B callbacks in real time.
Every payment is automatically matched to a tenant by account reference,
recorded in Postgres, and surfaced via a WhatsApp bot — no login, no app, no
spreadsheet. A single landlord manages their own units. An agent manages
multiple landlords from the same WhatsApp number with scoped commands.
