-- migrations/remove_apartment_name.sql
ALTER TABLE landlords
    DROP COLUMN IF EXISTS apartment_name;