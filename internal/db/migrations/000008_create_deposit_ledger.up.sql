BEGIN;

CREATE TABLE IF NOT EXISTS deposits (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    unit_id             UUID        NOT NULL REFERENCES units(id) ON DELETE CASCADE,
    landlord_id         UUID        NOT NULL REFERENCES landlords(id) ON DELETE CASCADE,
    deposit_expected    INTEGER     NOT NULL DEFAULT 0,
    deposit_paid        INTEGER     NOT NULL DEFAULT 0,
    deposit_balance     INTEGER     NOT NULL DEFAULT 0,
    deposit_refunded    INTEGER     NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (unit_id)
);

CREATE INDEX IF NOT EXISTS idx_deposits_landlord
    ON deposits (landlord_id);

CREATE TABLE IF NOT EXISTS deposit_transactions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    deposit_id  UUID        NOT NULL REFERENCES deposits(id) ON DELETE CASCADE,
    unit_id     UUID        NOT NULL REFERENCES units(id) ON DELETE CASCADE,
    landlord_id UUID        NOT NULL REFERENCES landlords(id) ON DELETE CASCADE,
    txn_type    TEXT        NOT NULL,
    amount      INTEGER     NOT NULL,
    note        TEXT        NOT NULL DEFAULT '',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    recorded_by TEXT        NOT NULL DEFAULT 'landlord'
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'deposit_transactions_type_check'
    ) THEN
        ALTER TABLE deposit_transactions
            ADD CONSTRAINT deposit_transactions_type_check
            CHECK (txn_type IN ('received', 'refund', 'adjustment'));
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_deposit_txns_deposit
    ON deposit_transactions (deposit_id, recorded_at DESC);

CREATE INDEX IF NOT EXISTS idx_deposit_txns_landlord
    ON deposit_transactions (landlord_id, recorded_at DESC);

COMMIT;