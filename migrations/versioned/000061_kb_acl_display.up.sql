-- Migration: 000061_kb_acl_display
-- Adds a display-only "source" store for a KB's roster, so the frontend can
-- render the ORIGINAL groups/usids a user picked (e.g. "group:teamA") instead
-- of the flat list of expanded usids that kb_acl actually stores.
--
-- Why a separate table (see docs/permission-api-and-mcp-usage.md §3.1):
--   The app expands groups into individual usids before calling
--   POST /permissions/kb-acl/batch, so kb_acl only ever holds usids and the
--   original "group" label is lost. This table keeps that label purely for
--   DISPLAY. It is NEVER consulted for permission decisions — those always run
--   off kb_acl's real usids. Because a group's membership can drift after the
--   grant, the stored source is a SNAPSHOT of what the user picked at save time.
--
-- One row per KB. The whole desired display structure (admins/normals as an
-- arbitrary list of group/usid tokens) is stored as JSONB, mirroring the
-- optional display_source field of the batch request. Absent row == the KB was
-- never saved with a display_source (older KBs / callers that don't send it),
-- in which case the API falls back to returning raw usids as before.
DO $$ BEGIN RAISE NOTICE '[Migration 000061] Creating kb_acl_display (display-only roster source)'; END $$;

CREATE TABLE IF NOT EXISTS kb_acl_display (
    kb_id      VARCHAR(36) PRIMARY KEY,
    source     JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$ BEGIN RAISE NOTICE '[Migration 000061] kb_acl_display created'; END $$;
