package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// toolDeps bundles the service dependencies and per-request context that MCP
// tool handlers need. A fresh instance is created per HTTP request with the
// authenticated tenant/user baked in.
type toolDeps struct {
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
	sessionService   interfaces.SessionService
	agentService     interfaces.CustomAgentService
	permService      interfaces.PermissionService
	caseService      interfaces.CaseService
	tenantID         uint64
	userID           string
}

// enrichCtx injects tenantID and userID into the context so that downstream
// service calls can find them. The MCP SDK creates its own context for tool
// handlers, which doesn't carry the gin middleware values.
func (d *toolDeps) enrichCtx(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, d.tenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, d.userID)
	return ctx
}

// resolveScope computes the set of knowledge base IDs a usid is allowed to
// query. It applies the permission scope (super_user -> all KBs; otherwise
// kb_acl grants ∪ common_kb) and, if the caller passed an explicit kb_ids
// list, intersects the two so a caller can never widen its own scope by
// passing arbitrary KB IDs.
//
// Returns the allowed KB IDs. An empty (non-nil) slice means "nothing visible"
// and the caller should short-circuit to an empty result.
func (d *toolDeps) resolveScope(
	ctx context.Context, usid string, requestedKBIDs []string,
) ([]string, error) {
	// super_user needs the full KB list; fetch it once. For non-super_users the
	// list is ignored by GetSearchScope, but fetching unconditionally keeps the
	// call simple and the KB count here is small.
	allKBs, err := d.kbService.ListKnowledgeBases(ctx)
	if err != nil {
		return nil, err
	}
	allKBIDs := make([]string, 0, len(allKBs))
	for _, kb := range allKBs {
		allKBIDs = append(allKBIDs, kb.ID)
	}

	scope, err := d.permService.GetSearchScope(ctx, usid, allKBIDs)
	if err != nil {
		return nil, err
	}

	// Intersect with the caller-requested subset if provided.
	if len(requestedKBIDs) > 0 {
		allowed := make(map[string]struct{}, len(scope))
		for _, id := range scope {
			allowed[id] = struct{}{}
		}
		filtered := make([]string, 0, len(requestedKBIDs))
		seen := make(map[string]struct{}, len(requestedKBIDs))
		for _, id := range requestedKBIDs {
			if id == "" {
				continue
			}
			if _, ok := allowed[id]; !ok {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			filtered = append(filtered, id)
		}
		return filtered, nil
	}

	// Filter scope against allKBIDs to remove stale ACL/common_kb references
	// that point to soft-deleted KBs. Without this, scope[0] may be a ghost ID
	// that passes GetSearchScope but is excluded by GORM's soft-delete filter in
	// GetKnowledgeBaseByIDs, causing pickPrimary to return nil → "knowledge base
	// not found" (error 1003).
	existing := make(map[string]struct{}, len(allKBIDs))
	for _, id := range allKBIDs {
		existing[id] = struct{}{}
	}
	live := scope[:0]
	for _, id := range scope {
		if _, ok := existing[id]; ok {
			live = append(live, id)
		}
	}
	return live, nil
}

// registerTools registers MCP tools on the given server.
func registerTools(server *mcpsdk.Server, deps *toolDeps) {
	// addKBList(server, deps)
	// addKBView(server, deps)
	// addDocList(server, deps)
	// addDocView(server, deps)
	// addDocDownload(server, deps)
	addSearchChunksFromSingleKB(server, deps)
	addSearchChunksFromAllKB(server, deps)
	addFindSimilarCases(server, deps)
	// addChat(server, deps)
	// addAgentList(server, deps)
	// addAgentInvoke(server, deps)
	// addChunkList(server, deps)
}

// ---- helpers ----------------------------------------------------------------

func bptr(b bool) *bool { return &b }

func errorResult(msg string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
	}
}

func successResult(payload any) *mcpsdk.CallToolResult {
	b, _ := json.Marshal(payload)
	return &mcpsdk.CallToolResult{
		StructuredContent: payload,
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}
}

// ---- kb_list ----------------------------------------------------------------

type kbListInput struct{}

func addKBList(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "kb_list",
		Description: "List all knowledge bases visible to the authenticated tenant. Returns items[]: each carries id, name, description, knowledge_count.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "List Knowledge Bases",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ kbListInput) (*mcpsdk.CallToolResult, any, error) {
		ctx = deps.enrichCtx(ctx)
		items, err := deps.kbService.ListKnowledgeBases(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list knowledge bases: %v", err)), nil, nil
		}
		type kbItem struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Description    string `json:"description"`
			KnowledgeCount int64  `json:"knowledge_count"`
		}
		out := make([]kbItem, 0, len(items))
		for _, kb := range items {
			out = append(out, kbItem{
				ID:             kb.ID,
				Name:           kb.Name,
				Description:    kb.Description,
				KnowledgeCount: kb.KnowledgeCount,
			})
		}
		return successResult(map[string]any{"items": out}), nil, nil
	})
}

// ---- kb_view ----------------------------------------------------------------

type kbViewInput struct {
	KBID string `json:"kb_id" jsonschema:"knowledge base ID"`
}

func addKBView(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "kb_view",
		Description: "Get detailed information about a knowledge base by ID, including chunking config, model IDs, knowledge count, and chunk count.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "View Knowledge Base",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in kbViewInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return errorResult("kb_id is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		kb, err := deps.kbService.GetKnowledgeBaseByID(ctx, in.KBID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get knowledge base: %v", err)), nil, nil
		}
		return successResult(kb), nil, nil
	})
}

// ---- doc_list ---------------------------------------------------------------

type docListInput struct {
	KBID     string `json:"kb_id" jsonschema:"knowledge base ID"`
	Page     int    `json:"page,omitempty" jsonschema:"1-indexed page number; defaults to 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"items per page (1..100); defaults to 20"`
	Status   string `json:"status,omitempty" jsonschema:"filter by parse status: pending | processing | completed | failed"`
	Keyword  string `json:"keyword,omitempty" jsonschema:"search keyword in file name"`
}

func addDocList(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_list",
		Description: "List documents in a knowledge base with optional pagination and filters (status, keyword).",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "List Documents",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docListInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return errorResult("kb_id is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		page := in.Page
		if page < 1 {
			page = 1
		}
		pageSize := in.PageSize
		if pageSize < 1 || pageSize > 100 {
			pageSize = 20
		}

		pagination := &types.Pagination{
			Page:     page,
			PageSize: pageSize,
		}
		filter := types.KnowledgeListFilter{
			ParseStatus: in.Status,
			Keyword:     in.Keyword,
		}

		result, err := deps.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx, in.KBID, pagination, filter)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list documents: %v", err)), nil, nil
		}
		return successResult(map[string]any{
			"items": result.Data,
			"total": result.Total,
			"page":  page,
		}), nil, nil
	})
}

// ---- doc_view ---------------------------------------------------------------

type docViewInput struct {
	DocID string `json:"doc_id" jsonschema:"document/knowledge ID"`
}

func addDocView(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_view",
		Description: "Get detailed information about a document by ID, including file name, title, parse status, file size, chunk count.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "View Document",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docViewInput) (*mcpsdk.CallToolResult, any, error) {
		if in.DocID == "" {
			return errorResult("doc_id is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		knowledge, err := deps.knowledgeService.GetKnowledgeByID(ctx, in.DocID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get document: %v", err)), nil, nil
		}
		return successResult(knowledge), nil, nil
	})
}

// ---- doc_download -----------------------------------------------------------

type docDownloadInput struct {
	DocID string `json:"doc_id" jsonschema:"document/knowledge ID"`
}

func addDocDownload(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_download",
		Description: "Download the raw content of a document. Returns the text content (up to 1MB). For large files, use search_chunks instead.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Download Document",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docDownloadInput) (*mcpsdk.CallToolResult, any, error) {
		if in.DocID == "" {
			return errorResult("doc_id is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		rc, filename, err := deps.knowledgeService.GetKnowledgeFile(ctx, in.DocID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to download document: %v", err)), nil, nil
		}
		defer rc.Close()

		const maxSize = 1 << 20 // 1 MiB
		data, err := io.ReadAll(io.LimitReader(rc, maxSize+1))
		if err != nil {
			return errorResult(fmt.Sprintf("failed to read file content: %v", err)), nil, nil
		}
		truncated := len(data) > maxSize
		if truncated {
			data = data[:maxSize]
		}

		return successResult(map[string]any{
			"filename":  filename,
			"content":   string(data),
			"truncated": truncated,
			"hint":      "If truncated, use search_chunks for targeted retrieval.",
		}), nil, nil
	})
}

// ---- search_chunks_from_single_kb -------------------------------------------

type searchFromSingleKBInput struct {
	KBName  string             `json:"kb_name,omitempty" jsonschema:"the knowledge base name to search in; provide either kb_name or kb_id"`
	KBID    string             `json:"kb_id,omitempty" jsonschema:"the knowledge base ID to search in; provide either kb_name or kb_id"`
	Query   string             `json:"query" jsonschema:"required; natural-language search query"`
	Limit   int                `json:"limit,omitempty" jsonschema:"max results (1..50); defaults to 10"`
	DocIDs  []string           `json:"doc_ids,omitempty" jsonschema:"optional: restrict search to specific document IDs within the KB"`
	Filters *types.CaseFilters `json:"filters,omitempty" jsonschema:"optional: strict Case attribute filters applied before semantic search"`
}

func addSearchChunksFromSingleKB(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_chunks_from_single_kb",
		Description: "Hybrid search (vector + keyword) within a single knowledge base identified by name or ID (provide either kb_name or kb_id). The user identity is determined by the X-User-Id header; the search is permitted only if the user has access to the specified KB. Returns the most relevant text chunks with scores.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Search Chunks (Single KB)",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchFromSingleKBInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBName == "" && in.KBID == "" {
			return errorResult("either kb_name or kb_id is required"), nil, nil
		}
		if in.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		usid := deps.userID
		if usid == "" {
			return errorResult("user identity is required: set X-User-Id header"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		limit := in.Limit
		if limit < 1 || limit > 50 {
			limit = 10
		}

		var kbID string
		if in.KBID != "" {
			kbID = in.KBID
		} else {
			// Resolve KB name to ID by listing all KBs and matching by name.
			allKBs, err := deps.kbService.ListKnowledgeBases(ctx)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to list knowledge bases: %v", err)), nil, nil
			}
			var matched []*struct{ id, name string }
			for _, kb := range allKBs {
				if strings.EqualFold(kb.Name, in.KBName) {
					matched = append(matched, &struct{ id, name string }{id: kb.ID, name: kb.Name})
				}
			}
			if len(matched) == 0 {
				return errorResult(fmt.Sprintf("knowledge base %q not found", in.KBName)), nil, nil
			}
			if len(matched) > 1 {
				ids := make([]string, len(matched))
				for i, m := range matched {
					ids[i] = m.id
				}
				return errorResult(fmt.Sprintf(
					"multiple knowledge bases found with name %q (ids: %s); please use kb_id or search_chunks_from_all_kb",
					in.KBName, strings.Join(ids, ", "),
				)), nil, nil
			}
			kbID = matched[0].id
		}

		// Verify the user has permission for this specific KB.
		scope, err := deps.resolveScope(ctx, usid, []string{kbID})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to resolve search scope: %v", err)), nil, nil
		}
		if len(scope) == 0 {
			kbRef := in.KBName
			if kbRef == "" {
				kbRef = in.KBID
			}
			return errorResult(fmt.Sprintf("access denied: user %q does not have permission to search KB %q", usid, kbRef)), nil, nil
		}

		return doSearch(ctx, deps, scope, in.Query, limit, in.DocIDs, in.Filters)
	})
}

// ---- search_chunks_from_all_kb ----------------------------------------------

type searchFromAllKBInput struct {
	Query   string             `json:"query" jsonschema:"required; natural-language search query"`
	Limit   int                `json:"limit,omitempty" jsonschema:"max results (1..50); defaults to 10"`
	DocIDs  []string           `json:"doc_ids,omitempty" jsonschema:"optional: restrict search to specific document IDs"`
	Filters *types.CaseFilters `json:"filters,omitempty" jsonschema:"optional: strict Case attribute filters applied before semantic search"`
}

func addSearchChunksFromAllKB(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_chunks_from_all_kb",
		Description: "Hybrid search (vector + keyword) across ALL knowledge bases the current user has permission to access. The user identity is determined by the X-User-Id header. Returns the most relevant text chunks with scores; each chunk carries its source kb_id.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Search Chunks (All KBs)",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchFromAllKBInput) (*mcpsdk.CallToolResult, any, error) {
		if in.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		usid := deps.userID
		if usid == "" {
			return errorResult("user identity is required: set X-User-Id header"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		limit := in.Limit
		if limit < 1 || limit > 50 {
			limit = 10
		}

		// Resolve full permission scope for this user.
		scope, err := deps.resolveScope(ctx, usid, nil)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to resolve search scope: %v", err)), nil, nil
		}
		if len(scope) == 0 {
			return successResult(map[string]any{"results": []any{}, "total": 0}), nil, nil
		}

		return doSearch(ctx, deps, scope, in.Query, limit, in.DocIDs, in.Filters)
	})
}

// ---- shared search logic ----------------------------------------------------

// doSearch executes hybrid search across the given KB scope and returns merged,
// globally-ranked results. Extracted from the original search_chunks to be
// shared by both search_chunks_from_single_kb and search_chunks_from_all_kb.
func doSearch(
	ctx context.Context, deps *toolDeps,
	scope []string, query string, limit int, docIDs []string, filters *types.CaseFilters,
) (*mcpsdk.CallToolResult, any, error) {
	if filters != nil && !filters.Empty() {
		caseIDs, err := deps.caseService.FilterKnowledgeIDs(ctx, scope, *filters)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to filter Cases: %v", err)), nil, nil
		}
		docIDs = intersectKnowledgeIDs(docIDs, caseIDs)
		if len(docIDs) == 0 {
			return emptySearchResult(), nil, nil
		}
	}

	// Pre-filter: if the caller didn't specify doc_ids, try to match
	// knowledge items by title/filename so that structured identifiers
	// (e.g. "70-PE510-16") that live only in document metadata can be
	// found without relying on BM25 tokenization of chunk content.
	if len(docIDs) == 0 {
		scopes := make([]types.KnowledgeSearchScope, len(scope))
		for i, kbID := range scope {
			scopes[i] = types.KnowledgeSearchScope{TenantID: deps.tenantID, KBID: kbID}
		}
		// Try full query
		if matched, _, err := deps.knowledgeService.SearchKnowledgeForScopes(
			ctx, scopes, query, 0, 20, nil,
		); err == nil && len(matched) > 0 {
			docIDs = make([]string, len(matched))
			for i, k := range matched {
				docIDs[i] = k.ID
			}
		}
		// If full query didn't match, extract doc-number-like tokens and retry
		if len(docIDs) == 0 {
			tokens := extractDocIdentifiers(query)
			for _, token := range tokens {
				if matched, _, err := deps.knowledgeService.SearchKnowledgeForScopes(
					ctx, scopes, token, 0, 20, nil,
				); err == nil && len(matched) > 0 {
					docIDs = make([]string, 0, len(matched))
					for _, k := range matched {
						docIDs = append(docIDs, k.ID)
					}
					break
				}
			}
		}
	}

	type chunkResult struct {
		ID             string  `json:"id"`
		KBID           string  `json:"kb_id"`
		Content        string  `json:"content"`
		KnowledgeID    string  `json:"knowledge_id"`
		KnowledgeTitle string  `json:"knowledge_title"`
		Score          float64 `json:"score"`
		ChunkIndex     int     `json:"chunk_index"`
	}

	// Single HybridSearch across all KBs in scope. By passing all KB IDs
	// into one call, vector and keyword results are pooled before RRF
	// fusion, producing a globally consistent ranking instead of merging
	// independently-ranked per-KB lists.
	params := types.SearchParams{
		QueryText:             query,
		MatchCount:            limit,
		KnowledgeIDs:          docIDs,
		KnowledgeBaseIDs:      scope,
		SkipContextEnrichment: true,
	}
	hits, err := deps.kbService.HybridSearch(ctx, scope[0], params)
	if err != nil {
		return errorResult(fmt.Sprintf("search failed: %v", err)), nil, nil
	}

	all := make([]chunkResult, 0, len(hits))
	for _, r := range hits {
		content := r.Content
		if r.ImageInfo != "" {
			content = searchutil.EnrichContentWithImageInfo(content, r.ImageInfo)
		}
		all = append(all, chunkResult{
			ID:             r.ID,
			KBID:           r.KnowledgeBaseID,
			Content:        content,
			KnowledgeID:    r.KnowledgeID,
			KnowledgeTitle: r.KnowledgeTitle,
			Score:          r.Score,
			ChunkIndex:     r.ChunkIndex,
		})
	}

	return successResult(map[string]any{
		"results": all,
		"total":   len(all),
	}), nil, nil
}

func intersectKnowledgeIDs(requested, candidates []string) []string {
	if len(candidates) == 0 {
		return []string{}
	}
	candidateSet := make(map[string]struct{}, len(candidates))
	for _, id := range candidates {
		candidateSet[id] = struct{}{}
	}
	if len(requested) == 0 {
		result := make([]string, 0, len(candidates))
		seen := make(map[string]struct{}, len(candidates))
		for _, id := range candidates {
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
		}
		return result
	}
	result := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		if _, ok := candidateSet[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func emptySearchResult() *mcpsdk.CallToolResult {
	return successResult(map[string]any{"results": []any{}, "total": 0})
}

// ---- agent_list -------------------------------------------------------------

type agentListInput struct{}

func addAgentList(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "agent_list",
		Description: "List all custom agents available in the current tenant. Returns agent ID, name, description.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "List Agents",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ agentListInput) (*mcpsdk.CallToolResult, any, error) {
		ctx = deps.enrichCtx(ctx)
		agents, err := deps.agentService.ListAgents(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list agents: %v", err)), nil, nil
		}
		type agentItem struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		out := make([]agentItem, 0, len(agents))
		for _, a := range agents {
			out = append(out, agentItem{
				ID:          a.ID,
				Name:        a.Name,
				Description: a.Description,
			})
		}
		return successResult(map[string]any{"items": out}), nil, nil
	})
}

// ---- chunk_list -------------------------------------------------------------

type chunkListInput struct {
	DocID    string `json:"doc_id" jsonschema:"document/knowledge ID"`
	Page     int    `json:"page,omitempty" jsonschema:"1-indexed page number; defaults to 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"items per page (1..100); defaults to 20"`
}

func addChunkList(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "chunk_list",
		Description: "List all chunks (text segments) of a specific document. Useful for debugging or inspecting how a document was split.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "List Chunks",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in chunkListInput) (*mcpsdk.CallToolResult, any, error) {
		if in.DocID == "" {
			return errorResult("doc_id is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		page := in.Page
		if page < 1 {
			page = 1
		}
		pageSize := in.PageSize
		if pageSize < 1 || pageSize > 100 {
			pageSize = 20
		}

		pagination := &types.Pagination{
			Page:     page,
			PageSize: pageSize,
		}

		result, err := deps.chunkService.ListPagedChunksByKnowledgeID(ctx, in.DocID, pagination, nil)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list chunks: %v", err)), nil, nil
		}

		return successResult(map[string]any{
			"items": result.Data,
			"total": result.Total,
			"page":  page,
		}), nil, nil
	})
}

// reDocIdentifier matches document-number-like patterns commonly used as
// filenames/titles: alphanumeric segments joined by hyphens, with at least
// one digit and one hyphen (e.g. "70-PE510-16", "80-42985-1881").
var reDocIdentifier = regexp.MustCompile(`[A-Za-z0-9]+(?:-[A-Za-z0-9]+)+`)

// reNumericID matches long numeric strings (≥6 digits) that might be part of
// a document identifier (e.g. "260323225542" from "KBA-260323225542").
var reNumericID = regexp.MustCompile(`[0-9]{6,}`)

// extractDocIdentifiers pulls document-number-like tokens from a query string.
// Returns them longest-first so the most specific identifier is tried first.
func extractDocIdentifiers(query string) []string {
	matches := reDocIdentifier.FindAllString(query, -1)
	// Filter: must contain at least one digit to be a plausible doc number
	var results []string
	for _, m := range matches {
		hasDigit := false
		for _, c := range m {
			if c >= '0' && c <= '9' {
				hasDigit = true
				break
			}
		}
		if hasDigit && len(m) >= 4 {
			results = append(results, m)
		}
	}
	// Also extract long numeric sequences (e.g. "260323225542" without hyphens)
	numMatches := reNumericID.FindAllString(query, -1)
	for _, m := range numMatches {
		results = append(results, m)
	}
	// Sort longest first (more specific identifiers first)
	sort.Slice(results, func(i, j int) bool {
		return len(results[i]) > len(results[j])
	})
	return results
}

// ---- find_similar_cases -----------------------------------------------------

type findSimilarCasesInput struct {
	CaseNumber string `json:"case_number,omitempty" jsonschema:"optional; source case number to find similar cases for (e.g. '12345678'). The case's subject and description are used as the search query. Takes priority over query when both are provided"`
	Query      string `json:"query,omitempty" jsonschema:"optional; free-text description of the issue or symptom to search for similar historical cases (e.g. 'FBRX calibration failure on SM8650'). Required when case_number is not provided"`
	Limit      int    `json:"limit,omitempty" jsonschema:"max number of similar cases to return (1..20); defaults to 5"`
}

func addFindSimilarCases(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "find_similar_cases",
		Description: `Find historical support cases that are semantically similar to a given case or issue description, to assist in troubleshooting and resolution. Searches only the Cases knowledge base.

WHEN TO USE:
- Finding past cases similar to a current issue ("Find similar cases for case 12345678")
- Checking whether an issue has occurred before ("Has this problem happened before?")
- Searching for historical resolutions or workarounds by symptom ("Find similar cases about FBRX calibration failure")
- Identifying recurring patterns across historical cases

HOW IT WORKS:
- If case_number is provided: looks up that case's subject and description, then searches for semantically similar cases
- If only query is provided: uses the query directly as the search input
- Results are deduplicated by case and include: case_number, subject, problem_area_1/2/3, chipsets, case_url, problem_summary (what the customer experienced), solution_summary (how it was resolved)

INPUTS: provide case_number OR query (or both; case_number takes priority for query construction).

OUTPUT FORMAT: For each result, always present ALL of the following fields to the user:
- Case number and subject (as a clickable link using case_url if available)
- Chipset and problem areas
- problem_summary: the full problem description — show this completely, do not omit or truncate
- solution_summary: the full resolution — show this completely, do not omit or truncate
Present each case as a clearly separated section, not just a table row. A table is acceptable for the overview but each case's problem_summary and solution_summary must be shown in full below the table.`,
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Find Similar Cases",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in findSimilarCasesInput) (*mcpsdk.CallToolResult, any, error) {
		if in.CaseNumber == "" && in.Query == "" {
			return errorResult("either case_number or query is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)

		// Find the Cases KB by name within this tenant.
		allKBs, err := deps.kbService.ListKnowledgeBases(ctx)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list knowledge bases: %v", err)), nil, nil
		}
		var casesKBID string
		for _, kb := range allKBs {
			if strings.EqualFold(kb.Name, "Cases") {
				casesKBID = kb.ID
				break
			}
		}
		if casesKBID == "" {
			return errorResult("Cases knowledge base not found in this tenant"), nil, nil
		}
		scope := []string{casesKBID}

		// Build the search query.
		searchQuery := in.Query
		var excludeKnowledgeID string

		if in.CaseNumber != "" {
			caseDocIDs, ferr := deps.caseService.FilterKnowledgeIDs(ctx, scope, types.CaseFilters{
				CaseNumber: in.CaseNumber,
			})
			if ferr != nil {
				return errorResult(fmt.Sprintf("failed to look up case %s: %v", in.CaseNumber, ferr)), nil, nil
			}
			if len(caseDocIDs) > 0 {
				excludeKnowledgeID = caseDocIDs[0]
				knowledge, kerr := deps.knowledgeService.GetKnowledgeByID(ctx, excludeKnowledgeID)
				if kerr == nil && knowledge != nil {
					parts := []string{}
					if knowledge.Title != "" {
						parts = append(parts, knowledge.Title)
					}
					if knowledge.Description != "" {
						parts = append(parts, knowledge.Description)
					}
					if len(parts) > 0 {
						searchQuery = strings.Join(parts, " ")
					}
				}
			}
			if searchQuery == "" {
				return errorResult(fmt.Sprintf("case %s not found", in.CaseNumber)), nil, nil
			}
		}

		limit := in.Limit
		if limit <= 0 || limit > 20 {
			limit = 5
		}

		// Search for chunks in the Cases KB. Fetch more than needed to allow for
		// deduplication and source-case exclusion.
		fetchLimit := (limit + 1) * 5
		params := types.SearchParams{
			QueryText:             searchQuery,
			MatchCount:            fetchLimit,
			KnowledgeBaseIDs:      scope,
			SkipContextEnrichment: true,
		}
		hits, serr := deps.kbService.HybridSearch(ctx, casesKBID, params)
		if serr != nil {
			return errorResult(fmt.Sprintf("search failed: %v", serr)), nil, nil
		}

		// Deduplicate by knowledge_id. For each case keep the chunk that is both
		// high-scoring and content-rich. Pure search score can favour short
		// boilerplate chunks (e.g. "### Related Change Requests\n* None") that
		// are semantically close to many queries. We multiply the search score
		// by a content-quality factor that penalises short or boilerplate-only
		// chunks so that Summary / Solution / Key-Steps chunks win.
		type deduped struct {
			knowledgeID    string
			knowledgeTitle string
			content        string
			score          float64
			quality        float64 // composite score used for selection
		}
		seen := make(map[string]*deduped, len(hits))
		order := make([]string, 0, len(hits))
		for _, r := range hits {
			if r.KnowledgeID == excludeKnowledgeID {
				continue
			}
			content := r.Content
			if r.ImageInfo != "" {
				content = searchutil.EnrichContentWithImageInfo(content, r.ImageInfo)
			}
			quality := r.Score * chunkContentQuality(content)
			if existing, ok := seen[r.KnowledgeID]; ok {
				if quality > existing.quality {
					existing.content = content
					existing.score = r.Score
					existing.quality = quality
				}
			} else {
				seen[r.KnowledgeID] = &deduped{
					knowledgeID:    r.KnowledgeID,
					knowledgeTitle: r.KnowledgeTitle,
					content:        content,
					score:          r.Score,
					quality:        quality,
				}
				order = append(order, r.KnowledgeID)
			}
		}

		// Take up to limit unique cases.
		if len(order) > limit {
			order = order[:limit]
		}

		// Batch-fetch structured case metadata (case_number, subject, chipsets, url, …).
		caseByKnowledgeID := make(map[string]*types.Case, len(order))
		if len(order) > 0 {
			cases, cerr := deps.caseService.GetCasesByKnowledgeIDs(ctx, order)
			if cerr == nil {
				for _, c := range cases {
					caseByKnowledgeID[c.KnowledgeID] = c
				}
			}
		}

		type caseResult struct {
			CaseNumber      string   `json:"case_number,omitempty"`
			Subject         string   `json:"subject,omitempty"`
			ProblemArea1    string   `json:"problem_area_1,omitempty"`
			ProblemArea2    string   `json:"problem_area_2,omitempty"`
			ProblemArea3    string   `json:"problem_area_3,omitempty"`
			Chipsets        []string `json:"chipsets,omitempty"`
			CaseURL         string   `json:"case_url,omitempty"`
			ProblemSummary  string   `json:"problem_summary,omitempty"`
			SolutionSummary string   `json:"solution_summary,omitempty"`
			Score           float64  `json:"score"`
		}

		results := make([]caseResult, 0, len(order))
		for _, knID := range order {
			d := seen[knID]
			r := caseResult{Score: d.score}

			if c, ok := caseByKnowledgeID[knID]; ok {
				r.CaseNumber = c.CaseNumber
				r.Subject = c.Subject
				r.ProblemArea1 = c.ProblemArea1
				r.ProblemArea2 = c.ProblemArea2
				r.ProblemArea3 = c.ProblemArea3
				r.CaseURL = c.CaseURL
				if len(c.Chipsets) > 0 {
					chips := make([]string, 0, len(c.Chipsets))
					for _, ch := range c.Chipsets {
						chips = append(chips, ch.Chipset)
					}
					r.Chipsets = chips
				}
			}

			// Fetch all chunks for this case and extract the Overview and Solution
			// sections directly by chunk content rather than relying on search score.
			// This guarantees we always return the most informative sections.
			chunks, cerr := deps.chunkService.ListChunksByKnowledgeID(ctx, knID)
			if cerr == nil {
				sort.Slice(chunks, func(i, j int) bool {
					return chunks[i].ChunkIndex < chunks[j].ChunkIndex
				})
				for _, ch := range chunks {
					lower := strings.ToLower(ch.Content)
					if r.ProblemSummary == "" &&
						(strings.Contains(lower, "case overview") || strings.Contains(lower, "description")) {
						r.ProblemSummary = strings.TrimSpace(ch.Content)
					}
					if r.SolutionSummary == "" &&
						(strings.Contains(lower, "proposed final solution") || strings.Contains(lower, "key steps to solve")) {
						r.SolutionSummary = strings.TrimSpace(ch.Content)
					}
					if r.ProblemSummary != "" && r.SolutionSummary != "" {
						break
					}
				}
			}

			results = append(results, r)
		}

		return successResult(map[string]any{
			"results": results,
			"total":   len(results),
		}), nil, nil
	})
}

func chunkContentQuality(content string) float64 {
	trimmed := strings.TrimSpace(content)
	length := len(trimmed)
	if length < 80 {
		return 0.1
	}
	boilerplate := []string{
		"Related Change Requests",
		"None identified",
		"None reported",
		"None mentioned",
		"## Case Information",
	}
	lower := strings.ToLower(trimmed)
	for _, pat := range boilerplate {
		if strings.Contains(lower, strings.ToLower(pat)) && length < 300 {
			return 0.2
		}
	}
	reward := 1.0
	if strings.Contains(lower, "proposed final solution") ||
		strings.Contains(lower, "key steps to solve") ||
		strings.Contains(lower, "case overview") ||
		strings.Contains(lower, "root cause") {
		reward = 1.5
	}
	lengthBonus := 1.0
	if length > 500 {
		lengthBonus = 1.2
	} else if length > 200 {
		lengthBonus = 1.1
	}
	return min(reward*lengthBonus, 2.0)
}
