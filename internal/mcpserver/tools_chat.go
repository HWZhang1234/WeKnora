package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// ---- chat -------------------------------------------------------------------

type chatInput struct {
	KBID      string `json:"kb_id" jsonschema:"knowledge base ID to query"`
	Query     string `json:"query" jsonschema:"the question to ask"`
	SessionID string `json:"session_id,omitempty" jsonschema:"optional session ID for multi-turn conversation; omit for a new conversation"`
}

func addChat(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "chat",
		Description: "RAG question answering: ask a question against a knowledge base. The system retrieves relevant chunks and generates an answer with citations. Returns the answer text, referenced chunks, and session_id for follow-up questions.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Knowledge QA Chat",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    false, // creates session/messages
			IdempotentHint:  false,
			OpenWorldHint:   bptr(true),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in chatInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return errorResult("kb_id is required"), nil, nil
		}
		if in.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)

		// Create or reuse session
		sessionID := in.SessionID
		if sessionID == "" {
			session := &types.Session{
				ID:       uuid.New().String(),
				TenantID: deps.tenantID,
				Title:    truncateString(in.Query, 50),
			}
			created, err := deps.sessionService.CreateSession(ctx, session)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to create session: %v", err)), nil, nil
			}
			sessionID = created.ID
		}

		// Get session to pass to QA
		session, err := deps.sessionService.GetSession(ctx, sessionID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get session: %v", err)), nil, nil
		}

		// Build QA request
		qaReq := &types.QARequest{
			Session:            session,
			Query:              in.Query,
			KnowledgeBaseIDs:   []string{in.KBID},
			AssistantMessageID: uuid.New().String(),
			UserMessageID:      uuid.New().String(),
		}

		// Use event bus to collect the answer.
		// The streaming goroutine emits EventAgentFinalAnswer chunks asynchronously
		// AFTER KnowledgeQA returns, so we must wait for Done=true.
		eventBus := event.NewEventBus()
		collector := newChatEventCollector()
		collector.subscribe(eventBus)

		// Run KnowledgeQA — pipeline stages run synchronously but the
		// CHAT_COMPLETION_STREAM stage spawns a goroutine that emits answer
		// events asynchronously.
		err = deps.sessionService.KnowledgeQA(ctx, qaReq, eventBus)
		if err != nil {
			return errorResult(fmt.Sprintf("QA failed: %v", err)), nil, nil
		}

		// Wait for the streaming goroutine to finish (Done=true on final_answer)
		collector.waitDone(ctx)

		return successResult(map[string]any{
			"answer":     collector.getAnswer(),
			"references": collector.getReferences(),
			"session_id": sessionID,
		}), nil, nil
	})
}

// ---- agent_invoke -----------------------------------------------------------

type agentInvokeInput struct {
	AgentID   string `json:"agent_id" jsonschema:"ID of the agent to invoke"`
	Query     string `json:"query" jsonschema:"the question or instruction for the agent"`
	SessionID string `json:"session_id,omitempty" jsonschema:"optional session ID for multi-turn; omit for new conversation"`
}

func addAgentInvoke(server *mcpsdk.Server, deps *toolDeps) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "agent_invoke",
		Description: "Invoke a custom agent with a query. The agent has its own system prompt, tool access, and knowledge base scope. Returns the agent's response and session_id for follow-up.",
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           "Invoke Agent",
			DestructiveHint: bptr(false),
			ReadOnlyHint:    false,
			IdempotentHint:  false,
			OpenWorldHint:   bptr(true),
		},
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in agentInvokeInput) (*mcpsdk.CallToolResult, any, error) {
		if in.AgentID == "" {
			return errorResult("agent_id is required"), nil, nil
		}
		if in.Query == "" {
			return errorResult("query is required"), nil, nil
		}
		ctx = deps.enrichCtx(ctx)

		// Get agent config
		agent, err := deps.agentService.GetAgentByID(ctx, in.AgentID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get agent: %v", err)), nil, nil
		}

		// Create or reuse session
		sessionID := in.SessionID
		if sessionID == "" {
			session := &types.Session{
				ID:       uuid.New().String(),
				TenantID: deps.tenantID,
				Title:    truncateString(in.Query, 50),
			}
			created, err := deps.sessionService.CreateSession(ctx, session)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to create session: %v", err)), nil, nil
			}
			sessionID = created.ID
		}

		session, err := deps.sessionService.GetSession(ctx, sessionID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get session: %v", err)), nil, nil
		}

		// Build QA request with agent
		qaReq := &types.QARequest{
			Session:            session,
			Query:              in.Query,
			CustomAgent:        agent,
			KnowledgeBaseIDs:   agent.Config.KnowledgeBases,
			AssistantMessageID: uuid.New().String(),
			UserMessageID:      uuid.New().String(),
		}

		// Collect events
		eventBus := event.NewEventBus()
		collector := newChatEventCollector()
		collector.subscribe(eventBus)

		// Run AgentQA
		err = deps.sessionService.AgentQA(ctx, qaReq, eventBus)
		if err != nil {
			return errorResult(fmt.Sprintf("agent execution failed: %v", err)), nil, nil
		}

		// Wait for streaming to complete
		collector.waitDone(ctx)

		return successResult(map[string]any{
			"answer":     collector.getAnswer(),
			"references": collector.getReferences(),
			"session_id": sessionID,
		}), nil, nil
	})
}

// ---- chatEventCollector collects streaming events into a final result --------

type chatEventCollector struct {
	mu         sync.Mutex
	chunks     []string
	references []map[string]any
	done       chan struct{} // closed when streaming is complete
}

func newChatEventCollector() *chatEventCollector {
	return &chatEventCollector{
		done: make(chan struct{}),
	}
}

func (c *chatEventCollector) subscribe(eventBus *event.EventBus) {
	// Answer chunks come via EventAgentFinalAnswer (emitted by chat_completion_stream goroutine)
	eventBus.On(event.EventAgentFinalAnswer, func(ctx context.Context, evt event.Event) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			if data.Content != "" {
				c.chunks = append(c.chunks, data.Content)
			}
			if data.Done {
				select {
				case <-c.done:
				default:
					close(c.done)
				}
			}
		}
		return nil
	})

	// Also listen on EventChatStream for backward compatibility
	eventBus.On(event.EventChatStream, func(ctx context.Context, evt event.Event) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if data, ok := evt.Data.(event.ChatData); ok && data.StreamChunk != "" {
			c.chunks = append(c.chunks, data.StreamChunk)
		}
		return nil
	})

	// References come via references events (emitted synchronously before KnowledgeQA returns)
	eventBus.On(event.EventAgentReferences, func(ctx context.Context, evt event.Event) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if data, ok := evt.Data.(event.AgentReferencesData); ok {
			if refs, ok := data.References.([]*types.SearchResult); ok {
				for _, ref := range refs {
					c.references = append(c.references, map[string]any{
						"knowledge_id":    ref.KnowledgeID,
						"knowledge_title": ref.KnowledgeTitle,
						"content":         ref.Content,
						"score":           ref.Score,
					})
				}
			}
		}
		return nil
	})

	// Listen for errors — if an error arrives before Done, signal completion
	// to avoid hanging forever.
	eventBus.On(event.EventError, func(ctx context.Context, evt event.Event) error {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		return nil
	})
}

// waitDone blocks until the streaming goroutine signals completion (Done=true)
// or the context is cancelled.
func (c *chatEventCollector) waitDone(ctx context.Context) {
	select {
	case <-c.done:
	case <-ctx.Done():
	}
}

func (c *chatEventCollector) getAnswer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.chunks, "")
}

func (c *chatEventCollector) getReferences() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.references == nil {
		return []map[string]any{}
	}
	return c.references
}

// ---- helpers ----------------------------------------------------------------

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
