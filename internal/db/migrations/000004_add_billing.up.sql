ALTER TABLE landlords
    ADD COLUMN IF NOT EXISTS subscription_status TEXT        NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS billing_cycle_end   DATE,
    ADD COLUMN IF NOT EXISTS unit_count          INTEGER     NOT NULL DEFAULT 0;
 
CREATE TABLE IF NOT EXISTS subscription_payments (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    landlord_id    UUID        NOT NULL REFERENCES landlords(id) ON DELETE CASCADE,
    transaction_id TEXT        NOT NULL UNIQUE,
    amount         INTEGER     NOT NULL,
    paid_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period_start   DATE        NOT NULL,
    period_end     DATE        NOT NULL
);
 
CREATE INDEX IF NOT EXISTS idx_sub_payments_landlord
    ON subscription_payments (landlord_id);
 
CREATE INDEX IF NOT EXISTS idx_landlords_sub_status
    ON landlords (subscription_status);