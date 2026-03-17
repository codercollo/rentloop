CREATE TABLE IF NOT EXISTS receipts (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id     UUID        NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    landlord_id    UUID        NOT NULL REFERENCES landlords(id),
    unit_id        UUID        NOT NULL REFERENCES units(id),
    receipt_number TEXT        NOT NULL UNIQUE,
    storage_key    TEXT        NOT NULL,
    public_url     TEXT        NOT NULL,
    sent_to_phone  TEXT        NOT NULL,
    sent_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
 
CREATE INDEX IF NOT EXISTS idx_receipts_payment
    ON receipts (payment_id);
 
CREATE INDEX IF NOT EXISTS idx_receipts_landlord
    ON receipts (landlord_id);