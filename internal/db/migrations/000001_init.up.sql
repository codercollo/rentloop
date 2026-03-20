CREATE EXTENSION IF NOT EXISTS "pgcrypto";
 
CREATE TABLE IF NOT EXISTS landlords (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    whatsapp_phone TEXT        NOT NULL UNIQUE,
    name           TEXT        NOT NULL DEFAULT '',
    paybill_number TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
 
CREATE TABLE IF NOT EXISTS units (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    landlord_id    UUID        NOT NULL REFERENCES landlords(id) ON DELETE CASCADE,
    unit_ref       TEXT        NOT NULL,
    tenant_name    TEXT        NOT NULL,
    tenant_phone   TEXT        NOT NULL,
    expected_rent  INTEGER     NOT NULL,
    active         BOOLEAN     NOT NULL DEFAULT TRUE,
    effective_from DATE        NOT NULL DEFAULT CURRENT_DATE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (landlord_id, unit_ref)
);
 
CREATE TABLE IF NOT EXISTS payments (
    id             UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id TEXT          NOT NULL UNIQUE,
    unit_id        UUID          NOT NULL REFERENCES units(id),
    landlord_id    UUID          NOT NULL REFERENCES landlords(id),
    tenant_phone   TEXT          NOT NULL,
    amount         INTEGER       NOT NULL,
    status         TEXT          NOT NULL DEFAULT 'paid',
    month_key      TEXT          NOT NULL,
    receipt_url    TEXT          NOT NULL DEFAULT '',
    paid_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
 
CREATE INDEX IF NOT EXISTS idx_payments_landlord_month
    ON payments (landlord_id, month_key);
 
CREATE INDEX IF NOT EXISTS idx_payments_unit_month
    ON payments (unit_id, month_key);
 
CREATE INDEX IF NOT EXISTS idx_units_landlord_active
    ON units (landlord_id, active);