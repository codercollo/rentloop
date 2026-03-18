DROP INDEX IF EXISTS idx_landlords_sub_status;
DROP INDEX IF EXISTS idx_sub_payments_landlord;
DROP TABLE IF EXISTS subscription_payments;
 
ALTER TABLE landlords
    DROP COLUMN IF EXISTS subscription_status,
    DROP COLUMN IF EXISTS billing_cycle_end,
    DROP COLUMN IF EXISTS unit_count;