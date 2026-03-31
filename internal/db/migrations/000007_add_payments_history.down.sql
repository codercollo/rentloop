BEGIN;

DROP INDEX IF EXISTS idx_payments_unit_month_desc;
DROP INDEX IF EXISTS idx_payments_landlord_month_desc;

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS payments_source_check;

ALTER TABLE payments
    DROP COLUMN IF EXISTS payment_source,
    DROP COLUMN IF EXISTS arrears_carried,
    DROP COLUMN IF EXISTS expected_rent_snapshot;

COMMIT;