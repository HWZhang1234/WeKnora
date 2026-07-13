-- Migration: 000060_kb_permissions
-- Adds a usid-scoped permission layer for the MCP search_chunks / chat tools.
--
-- Context (see docs/search-chunks-permission-design.md):
--   WeKnora is accessed by trusted internal projects through a single shared
--   X-API-Key (= one tenant). Those callers can only supply a business user id
--   ("usid") as a tool argument. There is no application middle-layer to do
--   permission filtering, so the permission logic lives inside WeKnora and is
--   keyed on usid, not tenant.
--
-- Three tables, all "bolted on" beside the core schema (they do NOT modify
-- knowledge_bases). None carry tenant_id — the whole system runs under one
-- API key / one tenant, and the permission dimension is usid.
--
--   * kb_acl     — knowledge base <-> usid <-> role (admin | normal).
--                  Both roles can query; only admin can manage that KB's roster.
--   * super_user — usids that implicitly have access to ALL knowledge bases,
--                  and can manage super_user / common_kb.
--   * common_kb  — knowledge bases that any usid may query regardless of ACL.
--
-- The three initial super_users (hanwzhan, whui, ksa) are seeded here so the
-- system is manageable from first boot (bootstrap problem: without a super_user
-- nobody can grant permissions).
DO $$ BEGIN RAISE NOTICE '[Migration 000060] Creating usid permission layer (kb_acl, super_user, common_kb)'; END $$;

CREATE TABLE IF NOT EXISTS kb_acl (
    id         BIGSERIAL PRIMARY KEY,
    kb_id      VARCHAR(36) NOT NULL,
    usid       VARCHAR(64) NOT NULL,
    role       VARCHAR(16) NOT NULL CHECK (role IN ('admin', 'normal')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_kb_acl_kb_usid UNIQUE (kb_id, usid)
);
CREATE INDEX IF NOT EXISTS idx_kb_acl_usid  ON kb_acl (usid);
CREATE INDEX IF NOT EXISTS idx_kb_acl_kb_id ON kb_acl (kb_id);

CREATE TABLE IF NOT EXISTS super_user (
    usid       VARCHAR(64) PRIMARY KEY,
    note       VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS common_kb (
    kb_id      VARCHAR(36) PRIMARY KEY,
    note       VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the first three super_users. ON CONFLICT DO NOTHING keeps the migration
-- idempotent and non-destructive if an operator has already added them.
INSERT INTO super_user (usid, note) VALUES
    ('hanwzhan', 'bootstrap super_user'),
    ('whui',     'bootstrap super_user'),
    ('ksa',      'bootstrap super_user')
ON CONFLICT (usid) DO NOTHING;

DO $$ BEGIN RAISE NOTICE '[Migration 000060] Permission layer created and seeded'; END $$;
