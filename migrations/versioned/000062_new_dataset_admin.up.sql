-- Migration: 000062_new_dataset_admin
-- Adds a table to track which usids are allowed to create new datasets (knowledge bases)
-- from external applications.
--
-- Like super_user / common_kb, this table is usid-scoped (no tenant_id).
DO $$ BEGIN RAISE NOTICE '[Migration 000062] Creating new_dataset_admin table'; END $$;

CREATE TABLE IF NOT EXISTS new_dataset_admin (
    usid       VARCHAR(64) PRIMARY KEY,
    note       VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$ BEGIN RAISE NOTICE '[Migration 000062] new_dataset_admin table created'; END $$;
