-- migrations/add_apartment_name.sql
ALTER TABLE landlords
    ADD COLUMN IF NOT EXISTS apartment_name TEXT NOT NULL DEFAULT '';