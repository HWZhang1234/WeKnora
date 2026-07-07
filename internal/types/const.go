package types

// ContextKey defines a type for context keys to avoid string collision
type ContextKey string

const (
	// TenantIDContextKey is the context key for tenant ID
	TenantIDContextKey ContextKey = "TenantID"
	// TenantInfoContextKey is the context key for tenant information
	TenantInfoContextKey ContextKey = "TenantInfo"
	// RequestIDContextKey is the context key for request ID
	RequestIDContextKey ContextKey = "RequestID"
	// LoggerContextKey is the context key for logger
	LoggerContextKey ContextKey = "Logger"
	// UserContextKey is the context key for user information
	UserContextKey ContextKey = "User"
	// UserIDContextKey is the context key for user ID
	UserIDContextKey ContextKey = "UserID"
	// TenantRoleContextKey is the context key for the caller's TenantRole
	// in the currently active tenant (loaded by the auth middleware from
	// the tenant_members table). See TenantRoleFromContext.
	TenantRoleContextKey ContextKey = "TenantRole"
	// SessionTenantIDContextKey is the context key for session owner's tenant ID.
	// When set (e.g. in pipeline with shared agent), session/message lookups use this instead of TenantIDContextKey.
	SessionTenantIDContextKey ContextKey = "SessionTenantID"
	// EmbedQueryContextKey is the context key for embedding query text
	EmbedQueryContextKey ContextKey = "EmbedQuery"
	// LanguageContextKey is the context key for user language preference (e.g. "zh-CN", "en-US")
	LanguageContextKey ContextKey = "Language"
	// LangfuseTraceContextKey carries the active Langfuse *Trace across the
	// request lifecycle. Defined here (not inside the langfuse package) so
	// that logger.CloneContext can preserve it without importing langfuse.
	LangfuseTraceContextKey ContextKey = "LangfuseTrace"
	// SystemAdminContextKey is the context key indicating whether the user is a system administrator
	SystemAdminContextKey ContextKey = "SystemAdmin"
	// LLMOverrideContextKey carries a per-request LLMOverride (caller-supplied
	// LLM API key and/or model name) from the chat handler down to
	// GetChatModel, so external callers can use their own LLM credentials and
	// pick a model per request without pre-creating a DB model record.
	// Never log the APIKey it carries.
	LLMOverrideContextKey ContextKey = "LLMOverride"
)

// LLMOverride holds per-request overrides for the chat LLM call. Empty fields
// mean "do not override" — the backend keeps the value from the DB model
// record. Injected into context by the chat handler and consumed at the single
// chokepoint ModelService.GetChatModel.
type LLMOverride struct {
	APIKey    string // Caller's LLM API key; empty = use the DB/model default.
	ModelName string // Override chat model name (must include provider prefix,
	// e.g. "anthropic::claude-4-8-opus"); empty = use the session default.
}

// String returns the string representation of the context key
func (c ContextKey) String() string {
	return string(c)
}
