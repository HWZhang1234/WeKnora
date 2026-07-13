// Package mcpserver implements an HTTP Streamable MCP Server endpoint that
// exposes WeKnora's knowledge base capabilities to external AI agents (e.g.
// CEBOT, Claude Desktop, Cursor). The endpoint is mounted at /mcp on the
// existing backend HTTP server (port 8080) and inherits global auth middleware
// (JWT Bearer / X-API-Key).
//
// Transport: MCP HTTP Streamable (spec 2025-03-26+) via go-sdk's
// StreamableHTTPHandler, which handles GET/POST/DELETE and session management.
package mcpserver

import (
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// MCPServerHandler adapts the MCP StreamableHTTPHandler to a Gin handler.
type MCPServerHandler struct {
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
	sessionService   interfaces.SessionService
	agentService     interfaces.CustomAgentService
	permService      interfaces.PermissionService
}

// NewMCPServerHandler creates a new MCP server handler with the required service
// dependencies injected via the DI container.
func NewMCPServerHandler(
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	sessionService interfaces.SessionService,
	agentService interfaces.CustomAgentService,
	permService interfaces.PermissionService,
) *MCPServerHandler {
	return &MCPServerHandler{
		kbService:        kbService,
		knowledgeService: knowledgeService,
		chunkService:     chunkService,
		sessionService:   sessionService,
		agentService:     agentService,
		permService:      permService,
	}
}

// Handle is the Gin handler that delegates to the MCP StreamableHTTPHandler.
// Each request creates a fresh MCP server with tools scoped to the authenticated
// tenant/user (extracted from gin context by the auth middleware).
func (h *MCPServerHandler) Handle(c *gin.Context) {
	// The StreamableHTTPHandler factory receives the *http.Request so it can
	// build a per-request MCP server. We extract tenant/user from gin context
	// which was populated by the auth middleware.
	handler := mcpsdk.NewStreamableHTTPHandler(
		func(r *http.Request) *mcpsdk.Server {
			// Gin stores values in the request context after middleware runs.
			ctx := r.Context()

			tenantID, _ := types.TenantIDFromContext(ctx)
			userID, _ := types.UserIDFromContext(ctx)

			server := mcpsdk.NewServer(
				&mcpsdk.Implementation{
					Name:    "weknora",
					Version: "1.0.0",
				},
				nil,
			)

			deps := &toolDeps{
				kbService:        h.kbService,
				knowledgeService: h.knowledgeService,
				chunkService:     h.chunkService,
				sessionService:   h.sessionService,
				agentService:     h.agentService,
				permService:      h.permService,
				tenantID:         tenantID,
				userID:           userID,
			}
			registerTools(server, deps)

			return server
		},
		&mcpsdk.StreamableHTTPOptions{
			// Stateless mode: each request is independent, no session tracking.
			// Simpler for API-key-based access where clients don't maintain
			// long-lived sessions.
			Stateless: true,
		},
	)

	// Delegate to the MCP HTTP handler
	handler.ServeHTTP(c.Writer, c.Request)
}
