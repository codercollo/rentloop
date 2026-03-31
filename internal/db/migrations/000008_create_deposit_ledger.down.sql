BEGIN;

-- Only drop table and everything else if table exists
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'deposit_transactions') THEN
        DROP INDEX IF EXISTS idx_deposit_txns_landlord;
        DROP INDEX IF EXISTS idx_deposit_txns_deposit;
        ALTER TABLE deposit_transactions
            DROP CONSTRAINT IF EXISTS deposit_transactions_type_check;
        DROP TABLE deposit_transactions;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'deposits') THEN
        DROP TABLE deposits;
    END IF;
END$$;

COMMIT;