package types

import "time"

// This file defines the usid-scoped permission layer models used by the MCP
// search_chunks / chat tools. See docs/search-chunks-permission-design.md and
// migration 000060 for the schema rationale.
//
// The permission dimension is "usid" (a business user id supplied by trusted
// internal callers), NOT tenant — the whole system runs under a single shared
// API key / single tenant. None of these tables carry tenant_id.

// KB ACL role values.
const (
	KBRoleAdmin  = "admin"  // can query the KB AND manage its roster
	KBRoleNormal = "normal" // can query the KB only
)

// IsValidKBRole reports whether r is an accepted kb_acl role.
func IsValidKBRole(r string) bool {
	return r == KBRoleAdmin || r == KBRoleNormal
}

// KBAcl is one (knowledge base, usid) -> role grant. UNIQUE(kb_id, usid)
// guarantees a usid holds at most one role per KB.
type KBAcl struct {
	ID        int64     `json:"id"         gorm:"column:id;primaryKey;autoIncrement"`
	KBID      string    `json:"kb_id"      gorm:"column:kb_id;type:varchar(36);not null"`
	Usid      string    `json:"usid"       gorm:"column:usid;type:varchar(64);not null"`
	Role      string    `json:"role"       gorm:"column:role;type:varchar(16);not null"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins the table to the migration's exact name.
func (KBAcl) TableName() string { return "kb_acl" }

// SuperUser is a usid with implicit access to ALL knowledge bases and the
// right to manage super_user / common_kb.
type SuperUser struct {
	Usid      string    `json:"usid"       gorm:"column:usid;type:varchar(64);primaryKey"`
	Note      string    `json:"note"       gorm:"column:note;type:varchar(255)"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName pins the table to the migration's exact name.
func (SuperUser) TableName() string { return "super_user" }

// CommonKB marks a knowledge base as public: any usid may query it regardless
// of its kb_acl entries.
type CommonKB struct {
	KBID      string    `json:"kb_id"      gorm:"column:kb_id;type:varchar(36);primaryKey"`
	Note      string    `json:"note"       gorm:"column:note;type:varchar(255)"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName pins the table to the migration's exact name.
func (CommonKB) TableName() string { return "common_kb" }

// KBAclDisplaySource is the display-only "source" of a KB's roster: the
// ORIGINAL group/usid tokens a user picked in the UI before the app expanded
// groups into individual usids. It is stored verbatim and returned for display;
// it is NEVER used for permission decisions (those run off kb_acl's real
// usids). Because a group's membership drifts over time, this is a SNAPSHOT
// taken at save time — the tokens may no longer match kb_acl's usids exactly.
//
// Tokens are opaque strings the caller defines, e.g. "group:teamA" or
// "usid:bob"; WeKnora does not parse or validate them.
type KBAclDisplaySource struct {
	Admins  []string `json:"admins"`
	Normals []string `json:"normals"`
}

// KBAclDisplay is the persisted display source for one KB (one row per KB).
// Source is stored as JSONB. Absent row == no display source was ever saved,
// and the API falls back to returning raw usids.
type KBAclDisplay struct {
	KBID      string    `json:"kb_id"      gorm:"column:kb_id;type:varchar(36);primaryKey"`
	Source    string    `json:"-"          gorm:"column:source;type:jsonb;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins the table to the migration's exact name.
func (KBAclDisplay) TableName() string { return "kb_acl_display" }
