package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// PermissionRepository is the storage contract for the usid permission layer
// (kb_acl / super_user / common_kb). See migration 000060 and
// docs/search-chunks-permission-design.md.
//
// None of these methods take a tenantID: the system runs under one shared API
// key, and permission is keyed on usid.
type PermissionRepository interface {
	// ---- kb_acl ----
	// UpsertKBAcl inserts or updates the (kb_id, usid) -> role grant.
	UpsertKBAcl(ctx context.Context, kbID, usid, role string) error
	// RemoveKBAcl deletes a (kb_id, usid) grant; returns whether a row was removed.
	RemoveKBAcl(ctx context.Context, kbID, usid string) (bool, error)
	// ListKBAclByKB lists all members of a KB.
	ListKBAclByKB(ctx context.Context, kbID string) ([]*types.KBAcl, error)
	// ListKBAclByUsid lists all KBs a usid has an explicit grant on.
	ListKBAclByUsid(ctx context.Context, usid string) ([]*types.KBAcl, error)
	// GetKBAcl returns the single (kb_id, usid) grant, or nil if none exists.
	GetKBAcl(ctx context.Context, kbID, usid string) (*types.KBAcl, error)
	// ListKBIDsByUsid returns just the kb_id list a usid has any grant on
	// (admin or normal). Hot path for building the search scope.
	ListKBIDsByUsid(ctx context.Context, usid string) ([]string, error)
	// ReplaceKBAcl atomically replaces a KB's entire roster with the given
	// (usid -> role) map (upsert listed members, delete the rest), in one tx.
	ReplaceKBAcl(ctx context.Context, kbID string, roles map[string]string) error

	// ---- kb_acl_display (display-only roster source) ----
	// UpsertKBAclDisplay stores/replaces the display source (raw JSON) for a KB.
	UpsertKBAclDisplay(ctx context.Context, kbID string, sourceJSON string) error
	// GetKBAclDisplay returns the stored display source JSON for a KB, or ""
	// (empty, no error) when none was ever saved.
	GetKBAclDisplay(ctx context.Context, kbID string) (string, error)

	// ---- super_user ----
	IsSuperUser(ctx context.Context, usid string) (bool, error)
	AddSuperUser(ctx context.Context, usid, note string) error
	RemoveSuperUser(ctx context.Context, usid string) (bool, error)
	ListSuperUsers(ctx context.Context) ([]*types.SuperUser, error)

	// ---- common_kb ----
	// ListCommonKBIDs returns just the kb_id list of public KBs.
	ListCommonKBIDs(ctx context.Context) ([]string, error)
	ListCommonKBs(ctx context.Context) ([]*types.CommonKB, error)
	AddCommonKB(ctx context.Context, kbID, note string) error
	RemoveCommonKB(ctx context.Context, kbID string) (bool, error)
}

// PermissionService wraps the repository with validation and the scope
// resolution / operator-authorization logic used by the MCP tools and the
// REST management endpoints.
type PermissionService interface {
	// GetSearchScope returns the set of knowledge base IDs a usid may query:
	//   super_user            -> all KB IDs
	//   otherwise             -> {KB in kb_acl for usid} ∪ {common_kb}
	// allKBIDs is the full KB list for the tenant (used only for super_users);
	// callers pass it in so the service stays decoupled from KnowledgeBaseService.
	GetSearchScope(ctx context.Context, usid string, allKBIDs []string) ([]string, error)

	// IsSuperUser reports whether usid is a super_user.
	IsSuperUser(ctx context.Context, usid string) (bool, error)
	// CanManageKB reports whether operator may manage kbID's roster
	// (operator is that KB's admin, or a super_user).
	CanManageKB(ctx context.Context, operator, kbID string) (bool, error)

	// ---- kb_acl management ----
	AddKBMember(ctx context.Context, kbID, usid, role string) error
	RemoveKBMember(ctx context.Context, kbID, usid string) error
	ListKBMembers(ctx context.Context, kbID string) ([]*types.KBAcl, error)
	ListKBsForUsid(ctx context.Context, usid string) ([]*types.KBAcl, error)
	// ReplaceKBMembers atomically replaces a KB's whole roster with the given
	// admin/normal usid lists (conflict -> admin; at least one admin required).
	// displaySource, when non-nil, is the ORIGINAL group/usid tokens the user
	// picked (display-only, stored verbatim, never used for permissions); pass
	// nil to leave any existing display source untouched.
	ReplaceKBMembers(ctx context.Context, kbID string, admins, normals []string, displaySource *types.KBAclDisplaySource) error
	// GetKBDisplaySource returns the stored display source for a KB, or nil
	// (no error) when none was saved. Callers use it to render the original
	// groups instead of the flat expanded usids.
	GetKBDisplaySource(ctx context.Context, kbID string) (*types.KBAclDisplaySource, error)

	// ---- super_user management ----
	AddSuperUser(ctx context.Context, usid, note string) error
	RemoveSuperUser(ctx context.Context, usid string) error
	ListSuperUsers(ctx context.Context) ([]*types.SuperUser, error)

	// ---- common_kb management ----
	AddCommonKB(ctx context.Context, kbID, note string) error
	RemoveCommonKB(ctx context.Context, kbID string) error
	ListCommonKBs(ctx context.Context) ([]*types.CommonKB, error)
}
