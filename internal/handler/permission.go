package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// PermissionHandler exposes the usid permission-layer management REST API:
// kb_acl (KB roster), super_user, and common_kb.
//
// Authorization model (see docs/search-chunks-permission-design.md §4.4):
// callers are trusted internal systems sharing one X-API-Key. Each management
// request self-asserts an operator_usid, which the handler validates against
// the permission tables:
//   - super_user operations require operator_usid ∈ super_user.
//   - kb_acl operations require operator_usid to be that KB's admin, or a super_user.
//
// common_kb reads are open to any authenticated caller; writes require super_user.
type PermissionHandler struct {
	svc       interfaces.PermissionService
	kbService interfaces.KnowledgeBaseService
}

func NewPermissionHandler(
	svc interfaces.PermissionService,
	kbService interfaces.KnowledgeBaseService,
) *PermissionHandler {
	return &PermissionHandler{svc: svc, kbService: kbService}
}

// mapPermError maps service sentinel errors to HTTP errors.
func mapPermError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, service.ErrPermEmptyUsid),
		stderrors.Is(err, service.ErrPermEmptyKBID),
		stderrors.Is(err, service.ErrPermInvalidRole),
		stderrors.Is(err, service.ErrPermNoAdmin):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
	}
}

// requireSuperUser validates operator ∈ super_user; writes an error and returns
// false when it isn't (or when the check fails).
func (h *PermissionHandler) requireSuperUser(c *gin.Context, operator string) bool {
	if operator == "" {
		c.Error(apperrors.NewBadRequestError("operator_usid is required"))
		return false
	}
	ok, err := h.svc.IsSuperUser(c.Request.Context(), operator)
	if err != nil {
		mapPermError(c, err)
		return false
	}
	if !ok {
		c.Error(apperrors.NewForbiddenError("operator is not a super_user"))
		return false
	}
	return true
}

// requireKBManager validates operator may manage kbID's roster.
func (h *PermissionHandler) requireKBManager(c *gin.Context, operator, kbID string) bool {
	if operator == "" {
		c.Error(apperrors.NewBadRequestError("operator_usid is required"))
		return false
	}
	ok, err := h.svc.CanManageKB(c.Request.Context(), operator, kbID)
	if err != nil {
		mapPermError(c, err)
		return false
	}
	if !ok {
		c.Error(apperrors.NewForbiddenError("operator is not an admin of this knowledge base"))
		return false
	}
	return true
}

// ---- kb_acl -----------------------------------------------------------------

// KBAclUpsertRequest is the body for POST /permissions/kb-acl.
type KBAclUpsertRequest struct {
	OperatorUsid string `json:"operator_usid"`
	KBID         string `json:"kb_id"`
	Usid         string `json:"usid"`
	Role         string `json:"role"` // admin | normal
}

// AddKBMember godoc
// @Summary      Add or update a KB member (admin/normal)
// @Description  Grant or update a usid's role on a knowledge base. Operator must be that KB's admin or a super_user.
// @Tags         Permission
// @Accept       json
// @Produce      json
// @Param        body  body      KBAclUpsertRequest  true  "operator_usid, kb_id, usid, role(admin|normal)"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  apperrors.AppError
// @Failure      403   {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/kb-acl [post]
func (h *PermissionHandler) AddKBMember(c *gin.Context) {
	var req KBAclUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !h.requireKBManager(c, req.OperatorUsid, req.KBID) {
		return
	}
	if err := h.svc.AddKBMember(c.Request.Context(), req.KBID, req.Usid, req.Role); err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// KBAclRemoveRequest is the body for DELETE /permissions/kb-acl.
type KBAclRemoveRequest struct {
	OperatorUsid string `json:"operator_usid"`
	KBID         string `json:"kb_id"`
	Usid         string `json:"usid"`
}

// RemoveKBMember godoc
// @Summary      Remove a KB member
// @Description  Remove a usid from a knowledge base roster. Operator must be that KB's admin or a super_user.
// @Tags         Permission
// @Accept       json
// @Produce      json
// @Param        body  body      KBAclRemoveRequest  true  "operator_usid, kb_id, usid"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  apperrors.AppError
// @Failure      403   {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/kb-acl [delete]
func (h *PermissionHandler) RemoveKBMember(c *gin.Context) {
	var req KBAclRemoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !h.requireKBManager(c, req.OperatorUsid, req.KBID) {
		return
	}
	if err := h.svc.RemoveKBMember(c.Request.Context(), req.KBID, req.Usid); err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListKBMembers godoc
// @Summary      List members of a KB, or KBs a usid can access
// @Description  Provide kb_id to list a KB's members (operator must be that KB's admin or a super_user), OR provide usid to list the KBs that usid can access (operator must be a super_user, or querying self).
// @Tags         Permission
// @Produce      json
// @Param        operator_usid  query     string  true   "operator's usid"
// @Param        kb_id          query     string  false  "list this KB's members (mode A)"
// @Param        usid           query     string  false  "list KBs accessible to this usid (mode B)"
// @Success      200            {object}  map[string]interface{}
// @Failure      400            {object}  apperrors.AppError
// @Failure      403            {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/kb-acl [get]
func (h *PermissionHandler) ListKBMembers(c *gin.Context) {
	ctx := c.Request.Context()
	operator := c.Query("operator_usid")
	kbID := c.Query("kb_id")
	usid := c.Query("usid")

	// Mode A: list members of a KB.
	if kbID != "" {
		if !h.requireKBManager(c, operator, kbID) {
			return
		}
		list, err := h.svc.ListKBMembers(ctx, kbID)
		if err != nil {
			mapPermError(c, err)
			return
		}
		// Echo the display source (original groups/usids the user picked), or
		// null when none was saved — the client can then render groups instead
		// of the flat usid list. Permissions still come from the usids in data.
		displaySource, err := h.svc.GetKBDisplaySource(ctx, kbID)
		if err != nil {
			mapPermError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": list, "display_source": displaySource})
		return
	}

	// Mode B: list KBs a usid can access. Allowed for a super_user, or the
	// usid querying itself.
	if usid != "" {
		if operator != usid {
			if !h.requireSuperUser(c, operator) {
				return
			}
		}
		list, err := h.svc.ListKBsForUsid(ctx, usid)
		if err != nil {
			mapPermError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
		return
	}

	c.Error(apperrors.NewBadRequestError("either kb_id or usid query parameter is required"))
}

// KBAclBatchRequest is the body for POST /permissions/kb-acl/batch.
// It carries the COMPLETE desired roster: admins and normals are the full
// lists (not a delta). The server resolves conflicts (a usid in both lists
// becomes admin) and replaces the KB's roster atomically.
type KBAclBatchRequest struct {
	OperatorUsid string   `json:"operator_usid"`
	KBID         string   `json:"kb_id"`
	Admins       []string `json:"admins"`
	Normals      []string `json:"normals"`
	// DisplaySource is OPTIONAL. It carries the ORIGINAL group/usid tokens the
	// user picked in the UI (e.g. "group:teamA", "usid:bob") before the caller
	// expanded groups into the individual usids sent in Admins/Normals. It is
	// stored verbatim and returned for display only — it is NEVER used for
	// permission decisions. Omit it to leave any existing display source
	// untouched (and to keep the old behaviour exactly).
	DisplaySource *types.KBAclDisplaySource `json:"display_source"`
}

// BatchSetKBMembers godoc
// @Summary      Replace a KB's whole member roster (admins + normals)
// @Description  Atomically set the complete member list of a knowledge base. Send the FULL desired lists on every save (not a delta): members not present are removed, listed members are inserted/updated. A usid appearing in both admins and normals becomes admin. At least one admin is required. Authorization: if the KB has no roster yet (freshly created, empty kb_acl) ANY authenticated caller may set the initial roster (bootstrap first-grant) — this is the "create database" flow where the creator picks the initial admins. Once the KB has a roster, the operator must be that KB's admin or a super_user. Optional display_source carries the ORIGINAL group/usid tokens the user picked (e.g. "group:teamA") before the caller expanded groups into the usids in admins/normals; it is stored verbatim and echoed back for display only, NEVER used for permission decisions. Intended for the create-database flow and the manage-members panel.
// @Tags         Permission
// @Accept       json
// @Produce      json
// @Param        body  body      KBAclBatchRequest  true  "operator_usid, kb_id, admins[], normals[]"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  apperrors.AppError
// @Failure      403   {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/kb-acl/batch [post]
func (h *PermissionHandler) BatchSetKBMembers(c *gin.Context) {
	var req KBAclBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	// Bootstrap first-grant: a freshly-created KB has an empty roster, so nobody
	// is its admin yet. Requiring "operator is already this KB's admin" would
	// deadlock the create-database flow (chicken-and-egg). So when the roster is
	// empty, any authenticated caller may set the initial members. Once a roster
	// exists, fall back to the normal admin/super_user check.
	existing, err := h.svc.ListKBMembers(c.Request.Context(), req.KBID)
	if err != nil {
		mapPermError(c, err)
		return
	}
	if len(existing) > 0 {
		if !h.requireKBManager(c, req.OperatorUsid, req.KBID) {
			return
		}
	}
	if err := h.svc.ReplaceKBMembers(c.Request.Context(), req.KBID, req.Admins, req.Normals, req.DisplaySource); err != nil {
		mapPermError(c, err)
		return
	}
	// Return the resulting roster so the client can refresh its view.
	list, err := h.svc.ListKBMembers(c.Request.Context(), req.KBID)
	if err != nil {
		mapPermError(c, err)
		return
	}
	// Also echo back the stored display source (may be nil) so the client can
	// re-render the original groups without a second request.
	displaySource, err := h.svc.GetKBDisplaySource(c.Request.Context(), req.KBID)
	if err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list, "display_source": displaySource})
}

// SearchableKB is one entry in the searchable-kbs response.
type SearchableKB struct {
	KBID   string `json:"kb_id"`
	Name   string `json:"name"`
	Source string `json:"source"`         // "acl" | "common" | "super"
	Role   string `json:"role,omitempty"` // "admin" | "normal" (only when source=="acl")
}

// ListSearchableKBs godoc
// @Summary      List all knowledge bases a usid can search
// @Description  Returns exactly the set of knowledge bases the given usid may search via the MCP search_chunks/chat tools: for a super_user, every KB; otherwise the KBs granted in kb_acl (admin or normal) UNION all public (common) KBs. Each entry carries the KB name and why it is visible (source: acl/common/super; role for acl entries). Use the returned kb_id as the kb_ids argument of search_chunks/chat to restrict a search to a single KB. Authorization: any authenticated caller may query their own usid; querying another usid requires the operator to be a super_user.
// @Tags         Permission
// @Produce      json
// @Param        usid           query     string  true   "the usid whose searchable KBs to list"
// @Param        operator_usid  query     string  false  "operator's usid; required (and must be a super_user) only when different from usid"
// @Success      200            {object}  map[string]interface{}
// @Failure      400            {object}  apperrors.AppError
// @Failure      403            {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/searchable-kbs [get]
func (h *PermissionHandler) ListSearchableKBs(c *gin.Context) {
	ctx := c.Request.Context()
	usid := c.Query("usid")
	operator := c.Query("operator_usid")
	if usid == "" {
		c.Error(apperrors.NewBadRequestError("usid query parameter is required"))
		return
	}
	// A caller may always query itself; querying another usid needs super_user.
	if operator != "" && operator != usid {
		if !h.requireSuperUser(c, operator) {
			return
		}
	}

	isSuper, err := h.svc.IsSuperUser(ctx, usid)
	if err != nil {
		mapPermError(c, err)
		return
	}

	result := make([]SearchableKB, 0)
	if isSuper {
		// super_user sees every KB.
		kbs, err := h.kbService.ListKnowledgeBases(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(apperrors.NewInternalServerError(err.Error()))
			return
		}
		for _, kb := range kbs {
			result = append(result, SearchableKB{KBID: kb.ID, Name: kb.Name, Source: "super"})
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
		return
	}

	// Non-super: kb_acl grants ∪ common_kb. Track source/role, acl wins over
	// common for naming purposes (a KB can be both granted and public).
	type meta struct {
		source string
		role   string
	}
	byID := make(map[string]meta)

	acls, err := h.svc.ListKBsForUsid(ctx, usid)
	if err != nil {
		mapPermError(c, err)
		return
	}
	for _, a := range acls {
		byID[a.KBID] = meta{source: "acl", role: a.Role}
	}

	commons, err := h.svc.ListCommonKBs(ctx)
	if err != nil {
		mapPermError(c, err)
		return
	}
	for _, ck := range commons {
		if _, exists := byID[ck.KBID]; !exists {
			byID[ck.KBID] = meta{source: "common"}
		}
	}

	if len(byID) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
		return
	}

	// Resolve names in one batch (tenant-agnostic: permission layer is by usid).
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	kbs, err := h.kbService.GetKnowledgeBasesByIDsOnly(ctx, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	names := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		names[kb.ID] = kb.Name
	}
	for id, m := range byID {
		result = append(result, SearchableKB{KBID: id, Name: names[id], Source: m.source, Role: m.role})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ---- super_user -------------------------------------------------------------

// SuperUserRequest is the body for POST /permissions/super-users.
type SuperUserRequest struct {
	OperatorUsid string `json:"operator_usid"`
	Usid         string `json:"usid"`
	Note         string `json:"note"`
}

// AddSuperUser godoc
// @Summary      Add a super_user
// @Description  Promote a usid to super_user. Operator must already be a super_user.
// @Tags         Permission
// @Accept       json
// @Produce      json
// @Param        body  body      SuperUserRequest  true  "operator_usid, usid, note"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  apperrors.AppError
// @Failure      403   {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/super-users [post]
func (h *PermissionHandler) AddSuperUser(c *gin.Context) {
	var req SuperUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !h.requireSuperUser(c, req.OperatorUsid) {
		return
	}
	if err := h.svc.AddSuperUser(c.Request.Context(), req.Usid, req.Note); err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveSuperUser godoc
// @Summary      Remove a super_user
// @Description  Demote a super_user. Operator must be a super_user.
// @Tags         Permission
// @Produce      json
// @Param        usid           path      string  true   "usid to remove"
// @Param        operator_usid  query     string  true   "operator's usid"
// @Success      200            {object}  map[string]interface{}
// @Failure      400            {object}  apperrors.AppError
// @Failure      403            {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/super-users/{usid} [delete]
func (h *PermissionHandler) RemoveSuperUser(c *gin.Context) {
	operator := c.Query("operator_usid")
	usid := c.Param("usid")
	if !h.requireSuperUser(c, operator) {
		return
	}
	if err := h.svc.RemoveSuperUser(c.Request.Context(), usid); err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListSuperUsers godoc
// @Summary      List all super_users
// @Description  Operator must be a super_user.
// @Tags         Permission
// @Produce      json
// @Param        operator_usid  query     string  true  "operator's usid"
// @Success      200            {object}  map[string]interface{}
// @Failure      400            {object}  apperrors.AppError
// @Failure      403            {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/super-users [get]
func (h *PermissionHandler) ListSuperUsers(c *gin.Context) {
	operator := c.Query("operator_usid")
	if !h.requireSuperUser(c, operator) {
		return
	}
	list, err := h.svc.ListSuperUsers(c.Request.Context())
	if err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// ---- common_kb --------------------------------------------------------------

// CommonKBRequest is the body for POST /permissions/common-kbs.
type CommonKBRequest struct {
	OperatorUsid string `json:"operator_usid"`
	KBID         string `json:"kb_id"`
	Note         string `json:"note"`
}

// AddCommonKB godoc
// @Summary      Mark a KB as public (common)
// @Description  Mark a knowledge base as common, making it readable by any usid. Operator must be a super_user.
// @Tags         Permission
// @Accept       json
// @Produce      json
// @Param        body  body      CommonKBRequest  true  "operator_usid, kb_id, note"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  apperrors.AppError
// @Failure      403   {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/common-kbs [post]
func (h *PermissionHandler) AddCommonKB(c *gin.Context) {
	var req CommonKBRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	if !h.requireSuperUser(c, req.OperatorUsid) {
		return
	}
	if err := h.svc.AddCommonKB(c.Request.Context(), req.KBID, req.Note); err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveCommonKB godoc
// @Summary      Unmark a public KB
// @Description  Remove a knowledge base's common (public) mark. Operator must be a super_user.
// @Tags         Permission
// @Produce      json
// @Param        kb_id          path      string  true   "KB id to unmark"
// @Param        operator_usid  query     string  true   "operator's usid"
// @Success      200            {object}  map[string]interface{}
// @Failure      400            {object}  apperrors.AppError
// @Failure      403            {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/common-kbs/{kb_id} [delete]
func (h *PermissionHandler) RemoveCommonKB(c *gin.Context) {
	operator := c.Query("operator_usid")
	kbID := c.Param("kb_id")
	if !h.requireSuperUser(c, operator) {
		return
	}
	if err := h.svc.RemoveCommonKB(c.Request.Context(), kbID); err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListCommonKBs godoc
// @Summary      List all public (common) KBs
// @Description  Open to any authenticated caller.
// @Tags         Permission
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  apperrors.AppError
// @Security     ApiKeyAuth
// @Router       /permissions/common-kbs [get]
func (h *PermissionHandler) ListCommonKBs(c *gin.Context) {
	list, err := h.svc.ListCommonKBs(c.Request.Context())
	if err != nil {
		mapPermError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}
