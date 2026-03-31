BEGIN;

ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS expected_rent_snapshot INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS arrears_carried INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS payment_source TEXT NOT NULL DEFAULT 'mpesa_stk';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'payments_source_check'
    ) THEN
        ALTER TABLE payments
            ADD CONSTRAINT payments_source_check
            CHECK (payment_source IN ('mpesa_stk', 'bank_paybill', 'manual'));
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_payments_unit_month_desc
    ON payments (unit_id, month_key DESC);

CREATE INDEX IF NOT EXISTS idx_payments_landlord_month_desc
    ON payments (landlord_id, month_key DESC);

COMMIT;