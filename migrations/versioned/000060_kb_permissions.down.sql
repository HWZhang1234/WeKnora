-- Reverse of 000060_kb_permissions.
-- Drops the usid permission layer entirely. This is destructive: all ACL
-- entries, super_user memberships, and common_kb markings are lost. That is
-- acceptable — the permission layer is additive metadata, not core data, and a
-- re-apply of the up migration re-seeds the bootstrap super_users.
DO $$ BEGIN RAISE NOTICE '[Migration 000060] Dropping usid permission layer'; END $$;

DROP TABLE IF EXISTS common_kb;
DROP TABLE IF EXISTS super_user;
DROP TABLE IF EXISTS kb_acl;

DO $$ BEGIN RAISE NOTICE '[Migration 000060] Permission layer dropped'; END $$;
