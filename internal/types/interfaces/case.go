package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// CaseService manages Case ingestion and structured search filters.
type CaseService interface {
	UpsertCase(ctx context.Context, kbID, caseNumber string, payload *types.UpsertCasePayload) (*types.UpsertCaseResult, error)
	FilterKnowledgeIDs(ctx context.Context, kbIDs []string, filters types.CaseFilters) ([]string, error)
}

// CaseRepository persists Case attributes separately from vector-store data.
type CaseRepository interface {
	GetByTenantAndCaseNumber(ctx context.Context, tenantID uint64, caseNumber string) (*types.Case, error)
	ExistsByKnowledgeID(ctx context.Context, knowledgeID string) (bool, error)
	Create(ctx context.Context, item *types.Case) error
	Update(ctx context.Context, item *types.Case, chipsets, changeRequests []string) error
	FilterKnowledgeIDs(ctx context.Context, tenantID uint64, kbIDs []string, filters types.CaseFilters) ([]string, error)
}
