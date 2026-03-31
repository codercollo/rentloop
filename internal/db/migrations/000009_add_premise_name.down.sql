BEGIN;

ALTER TABLE landlords
    DROP COLUMN IF EXISTS premise_name;

COMMIT;