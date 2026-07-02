package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

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

// registerTools registers all 10 MCP tools on the given server.
func registerTools(server *mcpsdk.Server, deps *toolDeps) {
	addKBList(server, deps)
	addKBView(server, deps)
	addDocList(server, deps)
	addDocView(server, deps)
	addDocDownload(server, deps)
	addSearchChunks(server, deps)
	addChat(server, deps)
	addAgentList(server, deps)
	addAgentInvoke(server, deps)
	addChunkList(server, deps)
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

// ---- search_chunks ----------------------------------------------------------

type searchChunksInput struct {
	KBID    string `json:"kb_id" jsonschema:"knowledge base ID to search"`
	Query   string `json:"query" jsonschema:"natural-language search query"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (1..50); defaults to 5"`
	DocIDs  []string `json:"doc_ids,omitempty" jsonschema:"optional: restrict search to specific document IDs"`
}

func addSearchChunks(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_chunks",
		Description: "Hybrid search (vector + keyword) across a knowledge base. Returns the most relevant text chunks with scores. Use this for answering questions from the knowledge base.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Search Chunks",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			OpenWorldHint:   bptr(false),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchChunksInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return errorResult("kb_id is required"), nil, nil
		}
		if in.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)
		limit := in.Limit
		if limit < 1 || limit > 50 {
			limit = 5
		}

		params := types.SearchParams{
			QueryText:    in.Query,
			MatchCount:   limit,
			KnowledgeIDs: in.DocIDs,
		}

		results, err := deps.kbService.HybridSearch(ctx, in.KBID, params)
		if err != nil {
			return errorResult(fmt.Sprintf("search failed: %v", err)), nil, nil
		}

		type chunkResult struct {
			ID             string  `json:"id"`
			Content        string  `json:"content"`
			KnowledgeID    string  `json:"knowledge_id"`
			KnowledgeTitle string  `json:"knowledge_title"`
			Score          float64 `json:"score"`
			ChunkIndex     int     `json:"chunk_index"`
		}
		out := make([]chunkResult, 0, len(results))
		for _, r := range results {
			out = append(out, chunkResult{
				ID:             r.ID,
				Content:        r.Content,
				KnowledgeID:    r.KnowledgeID,
				KnowledgeTitle: r.KnowledgeTitle,
				Score:          r.Score,
				ChunkIndex:     r.ChunkIndex,
			})
		}
		return successResult(map[string]any{
			"results": out,
			"total":   len(out),
		}), nil, nil
	})
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
