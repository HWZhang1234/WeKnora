"""
Patch script to bring an existing weknora.db SQLite database up to date
with the latest consolidated schema (migrations/sqlite/000000_init.up.sql).

Why: the SQLite migrator only ever runs the single 000000 init file, so
when that file is updated to include newer columns/tables, pre-existing
databases never receive the changes. This script applies the diff
idempotently using IF NOT EXISTS / column-presence checks.

Run:
    python scripts/patch_sqlite_schema.py [--db PATH]

Default DB path: ./data/weknora.db
"""

import argparse
import os
import sqlite3
import sys


def column_exists(cur, table, column):
    cur.execute(f"PRAGMA table_info({table})")
    return any(row[1] == column for row in cur.fetchall())


def table_exists(cur, table):
    cur.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name=?",
        (table,),
    )
    return cur.fetchone() is not None


PATCHES = []


def add_column_patch(table, column, ddl):
    def _patch(cur):
        if not table_exists(cur, table):
            print(f"  [skip] table {table} does not exist")
            return
        if column_exists(cur, table, column):
            print(f"  [ok]   {table}.{column} already exists")
            return
        print(f"  [add]  ALTER TABLE {table} ADD COLUMN {column} ...")
        cur.execute(f"ALTER TABLE {table} ADD COLUMN {column} {ddl}")

    _patch.__name__ = f"add_{table}_{column}"
    PATCHES.append(_patch)


def exec_patch(label, sql):
    def _patch(cur):
        print(f"  [run]  {label}")
        cur.executescript(sql)

    _patch.__name__ = label
    PATCHES.append(_patch)


# ---- Migration 000053: users.is_system_admin + system_settings ----
add_column_patch("users", "is_system_admin", "BOOLEAN NOT NULL DEFAULT 0")
exec_patch(
    "idx_users_is_system_admin",
    "CREATE INDEX IF NOT EXISTS idx_users_is_system_admin "
    "ON users(is_system_admin);",
)
exec_patch(
    "system_settings_table",
    """
CREATE TABLE IF NOT EXISTS system_settings (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    key              VARCHAR(128) NOT NULL UNIQUE,
    value            TEXT NOT NULL,
    value_type       VARCHAR(16)  NOT NULL,
    category         VARCHAR(32)  NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    is_secret        BOOLEAN NOT NULL DEFAULT 0,
    requires_restart BOOLEAN NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_system_settings_category
    ON system_settings (category);
""",
)

# ---- Migration 000054: tenant_invitations.token + accepted_count ----
add_column_patch("tenant_invitations", "token", "VARCHAR(64) NOT NULL DEFAULT ''")
add_column_patch(
    "tenant_invitations", "accepted_count", "INTEGER NOT NULL DEFAULT 0"
)
exec_patch(
    "idx_tenant_invitations_token",
    "CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_token "
    "ON tenant_invitations(token) WHERE token <> '' AND deleted_at IS NULL;",
)

# ---- Migration 000055: knowledge_processing_spans table ----
exec_patch(
    "knowledge_processing_spans_table",
    """
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id    VARCHAR(64)   NOT NULL,
    attempt         INTEGER       NOT NULL DEFAULT 1,
    span_id         VARCHAR(64)   NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(64)   NOT NULL,
    kind            VARCHAR(16)   NOT NULL,
    status          VARCHAR(16)   NOT NULL,
    input           TEXT,
    output          TEXT,
    metadata        TEXT,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    started_at      DATETIME,
    finished_at     DATETIME,
    duration_ms     BIGINT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_kpspan_attempt_span
    ON knowledge_processing_spans (knowledge_id, attempt, span_id);
CREATE INDEX IF NOT EXISTS idx_kpspan_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);
CREATE INDEX IF NOT EXISTS idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);
CREATE INDEX IF NOT EXISTS idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id)
    WHERE parent_span_id IS NOT NULL;
""",
)

# ---- Migration 000056: knowledges.pending_subtasks_count ----
add_column_patch(
    "knowledges", "pending_subtasks_count", "INTEGER NOT NULL DEFAULT 0"
)

# ---- Migration 000037: knowledge_bases.wiki_config + indexing_strategy ----
add_column_patch("knowledge_bases", "wiki_config", "TEXT")
add_column_patch("knowledge_bases", "indexing_strategy", "TEXT")
exec_patch(
    "backfill_indexing_strategy",
    "UPDATE knowledge_bases "
    "SET indexing_strategy = '{\"vector_enabled\":true,\"keyword_enabled\":true,"
    "\"wiki_enabled\":false,\"graph_enabled\":false}' "
    "WHERE indexing_strategy IS NULL OR indexing_strategy = '';",
)

# ---- Migration 000037: wiki_pages + wiki_page_issues tables ----
exec_patch(
    "wiki_pages_table",
    """
CREATE TABLE IF NOT EXISTS wiki_pages (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug            VARCHAR(255) NOT NULL,
    title           VARCHAR(512) NOT NULL DEFAULT '',
    page_type       VARCHAR(32) NOT NULL DEFAULT 'summary',
    status          VARCHAR(32) NOT NULL DEFAULT 'published',
    content         TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    source_refs     TEXT DEFAULT '[]',
    chunk_refs      TEXT DEFAULT '[]',
    in_links        TEXT DEFAULT '[]',
    out_links       TEXT DEFAULT '[]',
    page_metadata   TEXT DEFAULT '{}',
    aliases         TEXT DEFAULT '[]',
    version         INTEGER NOT NULL DEFAULT 1,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_pages_kb_slug
    ON wiki_pages (knowledge_base_id, slug)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wiki_pages_kb_id
    ON wiki_pages (knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_page_type
    ON wiki_pages (knowledge_base_id, page_type);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_tenant_id
    ON wiki_pages (tenant_id);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_deleted_at
    ON wiki_pages (deleted_at);
""",
)

exec_patch(
    "wiki_page_issues_table",
    """
CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    suspected_knowledge_ids TEXT,
    status VARCHAR(20) DEFAULT 'pending' NOT NULL,
    reported_by VARCHAR(100) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_tenant_id ON wiki_page_issues(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_knowledge_base_id ON wiki_page_issues(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_slug ON wiki_page_issues(slug);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_status ON wiki_page_issues(status);
""",
)

# ---- Migration 000040: wiki_log_entries ----
exec_patch(
    "wiki_log_entries_table",
    """
CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action            VARCHAR(32) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL DEFAULT '',
    doc_title         TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    pages_affected    TEXT NOT NULL DEFAULT '[]',
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_kb_id_desc
    ON wiki_log_entries (knowledge_base_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_wiki_log_entries_tenant_id
    ON wiki_log_entries (tenant_id);
""",
)

# ---- Migration 000034: messages.attachments ----
add_column_patch("messages", "attachments", "TEXT DEFAULT '[]'")

# ---- Migration 000041: task_pending_ops + task_dead_letters ----
exec_patch(
    "task_pending_ops_table",
    """
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    op          VARCHAR(32) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}',
    fail_count  INTEGER NOT NULL DEFAULT 0,
    enqueued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at  DATETIME
);
CREATE INDEX IF NOT EXISTS idx_task_pending_ops_scope
    ON task_pending_ops (task_type, scope, scope_id, id);
CREATE INDEX IF NOT EXISTS idx_task_pending_ops_tenant
    ON task_pending_ops (tenant_id);
""",
)

exec_patch(
    "task_dead_letters_table",
    """
CREATE TABLE IF NOT EXISTS task_dead_letters (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   BIGINT NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    related_id  VARCHAR(64) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL,
    last_error  TEXT NOT NULL DEFAULT '',
    fail_count  INTEGER NOT NULL,
    failed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_scope
    ON task_dead_letters (scope, scope_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_tenant
    ON task_dead_letters (tenant_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_task_type
    ON task_dead_letters (task_type, failed_at DESC);
""",
)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--db",
        default=os.path.join("data", "weknora.db"),
        help="Path to the SQLite database file",
    )
    args = parser.parse_args()

    if not os.path.exists(args.db):
        print(f"ERROR: database not found: {args.db}", file=sys.stderr)
        sys.exit(1)

    print(f"Patching database: {args.db}")
    conn = sqlite3.connect(args.db)
    try:
        cur = conn.cursor()
        for patch in PATCHES:
            patch(cur)
        conn.commit()
    finally:
        conn.close()
    print("Done.")


if __name__ == "__main__":
    main()