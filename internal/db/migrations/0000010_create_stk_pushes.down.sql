-- Drop index first (if it exists)
DROP INDEX IF EXISTS stk_pushes_mpesa_receipt_idx;

-- Then drop the table
DROP TABLE IF EXISTS stk_pushes;