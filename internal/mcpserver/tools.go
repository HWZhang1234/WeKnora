package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	return scope, nil
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
	KBName string   `json:"kb_name" jsonschema:"required; the knowledge base name to search in"`
	Query  string   `json:"query" jsonschema:"required; natural-language search query"`
	Limit  int      `json:"limit,omitempty" jsonschema:"max results (1..50); defaults to 10"`
	DocIDs []string `json:"doc_ids,omitempty" jsonschema:"optional: restrict search to specific document IDs within the KB"`
}

func addSearchChunksFromSingleKB(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_chunks_from_single_kb",
		Description: "Hybrid search (vector + keyword) within a single knowledge base identified by name. The user identity is determined by the X-User-Id header; the search is permitted only if the user has access to the specified KB. Returns the most relevant text chunks with scores.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Search Chunks (Single KB)",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchFromSingleKBInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBName == "" {
			return errorResult("kb_name is required"), nil, nil
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
				"multiple knowledge bases found with name %q (ids: %s); please use search_chunks_from_all_kb or contact admin to rename duplicates",
				in.KBName, strings.Join(ids, ", "),
			)), nil, nil
		}
		kbID := matched[0].id

		// Verify the user has permission for this specific KB.
		scope, err := deps.resolveScope(ctx, usid, []string{kbID})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to resolve search scope: %v", err)), nil, nil
		}
		if len(scope) == 0 {
			return errorResult(fmt.Sprintf("access denied: user %q does not have permission to search KB %q", usid, in.KBName)), nil, nil
		}

		return doSearch(ctx, deps, scope, in.Query, limit, in.DocIDs)
	})
}

// ---- search_chunks_from_all_kb ----------------------------------------------

type searchFromAllKBInput struct {
	Query  string   `json:"query" jsonschema:"required; natural-language search query"`
	Limit  int      `json:"limit,omitempty" jsonschema:"max results (1..50); defaults to 10"`
	DocIDs []string `json:"doc_ids,omitempty" jsonschema:"optional: restrict search to specific document IDs"`
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

		return doSearch(ctx, deps, scope, in.Query, limit, in.DocIDs)
	})
}

// ---- shared search logic ----------------------------------------------------

// doSearch executes hybrid search across the given KB scope and returns merged,
// globally-ranked results. Extracted from the original search_chunks to be
// shared by both search_chunks_from_single_kb and search_chunks_from_all_kb.
func doSearch(
	ctx context.Context, deps *toolDeps,
	scope []string, query string, limit int, docIDs []string,
) (*mcpsdk.CallToolResult, any, error) {
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

	// Fan out one single-KB HybridSearch per KB in scope, concurrently.
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		all     []chunkResult
		errs    []string
		okCount int
	)
	params := types.SearchParams{
		QueryText:    query,
		MatchCount:   limit,
		KnowledgeIDs: docIDs,
	}
	for _, kbID := range scope {
		wg.Add(1)
		go func(kbID string) {
			defer wg.Done()
			hits, serr := deps.kbService.HybridSearch(ctx, kbID, params)
			mu.Lock()
			defer mu.Unlock()
			if serr != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", kbID, serr))
				return
			}
			okCount++
			for _, r := range hits {
				content := r.Content
				if r.ImageInfo != "" {
					content = searchutil.EnrichContentWithImageInfo(content, r.ImageInfo)
				}
				all = append(all, chunkResult{
					ID:             r.ID,
					KBID:           kbID,
					Content:        content,
					KnowledgeID:    r.KnowledgeID,
					KnowledgeTitle: r.KnowledgeTitle,
					Score:          r.Score,
					ChunkIndex:     r.ChunkIndex,
				})
			}
		}(kbID)
	}
	wg.Wait()

	if okCount == 0 && len(errs) > 0 {
		return errorResult(fmt.Sprintf("search failed for all %d knowledge base(s): %s",
			len(errs), strings.Join(errs, "; "))), nil, nil
	}

	// Global sort by score desc, then truncate to the global top-K.
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Score > all[j].Score
	})
	if len(all) > limit {
		all = all[:limit]
	}

	return successResult(map[string]any{
		"results": all,
		"total":   len(all),
	}), nil, nil
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
