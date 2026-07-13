-- Reverse of 000061_kb_acl_display.
-- Drops the display-only roster source store. Destructive but safe: the data is
-- purely cosmetic (group labels for display); permissions live in kb_acl and are
-- untouched. After a drop, the API falls back to returning raw usids.
DO $$ BEGIN RAISE NOTICE '[Migration 000061] Dropping kb_acl_display'; END $$;

DROP TABLE IF EXISTS kb_acl_display;

DO $$ BEGIN RAISE NOTICE '[Migration 000061] kb_acl_display dropped'; END $$;
