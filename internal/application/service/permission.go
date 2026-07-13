package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Sentinel errors so handlers can map cleanly to HTTP status codes.
var (
	ErrPermEmptyUsid     = errors.New("usid is required")
	ErrPermEmptyKBID     = errors.New("kb_id is required")
	ErrPermInvalidRole   = errors.New("role must be 'admin' or 'normal'")
	ErrPermNotAuthorized = errors.New("operator is not authorized for this action")
	ErrPermNoAdmin       = errors.New("at least one admin is required")
)

// permissionService wraps PermissionRepository with validation and the scope
// resolution / operator-authorization logic. It intentionally does NOT depend
// on KnowledgeBaseService: the caller supplies the full KB list for super_user
// scope resolution, keeping this service free of import cycles.
type permissionService struct {
	repo interfaces.PermissionRepository
}

// NewPermissionService constructs the service.
func NewPermissionService(repo interfaces.PermissionRepository) interfaces.PermissionService {
	return &permissionService{repo: repo}
}

// GetSearchScope implements the scope formula from the design doc:
//
//	super_user -> all KB IDs
//	otherwise  -> {KB in kb_acl for usid} ∪ {common_kb}
//
// The result is de-duplicated. An empty result means "no visible KBs" and the
// caller should short-circuit to an empty search.
func (s *permissionService) GetSearchScope(
	ctx context.Context, usid string, allKBIDs []string,
) ([]string, error) {
	usid = strings.TrimSpace(usid)
	if usid == "" {
		return nil, ErrPermEmptyUsid
	}

	isSuper, err := s.repo.IsSuperUser(ctx, usid)
	if err != nil {
		return nil, err
	}
	if isSuper {
		// super_user sees everything; common_kb is a subset, no special-casing.
		return dedupe(allKBIDs), nil
	}

	aclIDs, err := s.repo.ListKBIDsByUsid(ctx, usid)
	if err != nil {
		return nil, err
	}
	commonIDs, err := s.repo.ListCommonKBIDs(ctx)
	if err != nil {
		return nil, err
	}
	return dedupe(append(aclIDs, commonIDs...)), nil
}

func (s *permissionService) IsSuperUser(ctx context.Context, usid string) (bool, error) {
	usid = strings.TrimSpace(usid)
	if usid == "" {
		return false, ErrPermEmptyUsid
	}
	return s.repo.IsSuperUser(ctx, usid)
}

// CanManageKB reports whether operator may manage kbID's roster: true if the
// operator is a super_user, or holds the 'admin' role on that KB.
func (s *permissionService) CanManageKB(ctx context.Context, operator, kbID string) (bool, error) {
	operator = strings.TrimSpace(operator)
	kbID = strings.TrimSpace(kbID)
	if operator == "" {
		return false, ErrPermEmptyUsid
	}
	if kbID == "" {
		return false, ErrPermEmptyKBID
	}
	isSuper, err := s.repo.IsSuperUser(ctx, operator)
	if err != nil {
		return false, err
	}
	if isSuper {
		return true, nil
	}
	acl, err := s.repo.GetKBAcl(ctx, kbID, operator)
	if err != nil {
		return false, err
	}
	return acl != nil && acl.Role == types.KBRoleAdmin, nil
}

// ---- kb_acl management ----

func (s *permissionService) AddKBMember(ctx context.Context, kbID, usid, role string) error {
	kbID = strings.TrimSpace(kbID)
	usid = strings.TrimSpace(usid)
	if kbID == "" {
		return ErrPermEmptyKBID
	}
	if usid == "" {
		return ErrPermEmptyUsid
	}
	if !types.IsValidKBRole(role) {
		return ErrPermInvalidRole
	}
	return s.repo.UpsertKBAcl(ctx, kbID, usid, role)
}

func (s *permissionService) RemoveKBMember(ctx context.Context, kbID, usid string) error {
	kbID = strings.TrimSpace(kbID)
	usid = strings.TrimSpace(usid)
	if kbID == "" {
		return ErrPermEmptyKBID
	}
	if usid == "" {
		return ErrPermEmptyUsid
	}
	_, err := s.repo.RemoveKBAcl(ctx, kbID, usid)
	return err
}

func (s *permissionService) ListKBMembers(ctx context.Context, kbID string) ([]*types.KBAcl, error) {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return nil, ErrPermEmptyKBID
	}
	return s.repo.ListKBAclByKB(ctx, kbID)
}

func (s *permissionService) ListKBsForUsid(ctx context.Context, usid string) ([]*types.KBAcl, error) {
	usid = strings.TrimSpace(usid)
	if usid == "" {
		return nil, ErrPermEmptyUsid
	}
	return s.repo.ListKBAclByUsid(ctx, usid)
}

// ReplaceKBMembers atomically replaces a KB's entire roster with the given
// admin/normal usid lists (the "save whole panel" operation). Semantics:
//   - A usid present in BOTH lists resolves to admin (admin ⊇ normal).
//   - Blank usids are ignored; the remaining lists are de-duplicated.
//   - At least one admin must remain (ErrPermNoAdmin otherwise) so a KB is
//     never left with no one able to manage it.
//   - Any current member NOT in the resulting set is removed.
//
// Operator authorization is enforced by the handler (CanManageKB) before this
// is called.
func (s *permissionService) ReplaceKBMembers(ctx context.Context, kbID string, admins, normals []string, displaySource *types.KBAclDisplaySource) error {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return ErrPermEmptyKBID
	}

	roles := make(map[string]string, len(admins)+len(normals))
	// Normals first, admins second so admin overwrites normal on conflict.
	for _, u := range normals {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		roles[u] = types.KBRoleNormal
	}
	adminCount := 0
	for _, u := range admins {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		roles[u] = types.KBRoleAdmin
	}
	for _, role := range roles {
		if role == types.KBRoleAdmin {
			adminCount++
		}
	}
	if adminCount == 0 {
		return ErrPermNoAdmin
	}

	if err := s.repo.ReplaceKBAcl(ctx, kbID, roles); err != nil {
		return err
	}

	// Persist the display-only source when the caller supplied one. This is
	// stored verbatim and NEVER used for permission decisions; it only lets the
	// frontend render the original groups instead of the flat expanded usids.
	if displaySource != nil {
		raw, err := json.Marshal(displaySource)
		if err != nil {
			return err
		}
		if err := s.repo.UpsertKBAclDisplay(ctx, kbID, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// GetKBDisplaySource returns the stored display source for a KB, or nil (no
// error) when none was saved.
func (s *permissionService) GetKBDisplaySource(ctx context.Context, kbID string) (*types.KBAclDisplaySource, error) {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return nil, ErrPermEmptyKBID
	}
	raw, err := s.repo.GetKBAclDisplay(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var src types.KBAclDisplaySource
	if err := json.Unmarshal([]byte(raw), &src); err != nil {
		return nil, err
	}
	return &src, nil
}

// ---- super_user management ----

func (s *permissionService) AddSuperUser(ctx context.Context, usid, note string) error {
	usid = strings.TrimSpace(usid)
	if usid == "" {
		return ErrPermEmptyUsid
	}
	return s.repo.AddSuperUser(ctx, usid, note)
}

func (s *permissionService) RemoveSuperUser(ctx context.Context, usid string) error {
	usid = strings.TrimSpace(usid)
	if usid == "" {
		return ErrPermEmptyUsid
	}
	_, err := s.repo.RemoveSuperUser(ctx, usid)
	return err
}

func (s *permissionService) ListSuperUsers(ctx context.Context) ([]*types.SuperUser, error) {
	return s.repo.ListSuperUsers(ctx)
}

// ---- common_kb management ----

func (s *permissionService) AddCommonKB(ctx context.Context, kbID, note string) error {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return ErrPermEmptyKBID
	}
	return s.repo.AddCommonKB(ctx, kbID, note)
}

func (s *permissionService) RemoveCommonKB(ctx context.Context, kbID string) error {
	kbID = strings.TrimSpace(kbID)
	if kbID == "" {
		return ErrPermEmptyKBID
	}
	_, err := s.repo.RemoveCommonKB(ctx, kbID)
	return err
}

func (s *permissionService) ListCommonKBs(ctx context.Context) ([]*types.CommonKB, error) {
	return s.repo.ListCommonKBs(ctx)
}

// dedupe returns the input with empty strings and duplicates removed, order
// preserved by first occurrence.
func dedupe(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
