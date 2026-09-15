package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

var (
	ErrCaseNumberRequired        = errors.New("case number is required")
	ErrCaseSummaryRequired       = errors.New("case summary is required")
	ErrInvalidCaseDate           = errors.New("case date must use YYYY-MM-DD format")
	ErrInvalidCaseDateRange      = errors.New("Case date filters must use YYYY-MM-DD format and date_from must not exceed date_to")
	ErrInvalidCasePayload        = errors.New("case payload contains a value that exceeds its allowed length")
	ErrCaseKnowledgeBaseMismatch = errors.New("case already belongs to a different knowledge base")
)

type caseService struct {
	repo             interfaces.CaseRepository
	knowledgeService interfaces.KnowledgeService
}

func NewCaseService(
	repo interfaces.CaseRepository,
	knowledgeService interfaces.KnowledgeService,
) interfaces.CaseService {
	return &caseService{repo: repo, knowledgeService: knowledgeService}
}

func (s *caseService) UpsertCase(
	ctx context.Context,
	kbID, caseNumber string,
	payload *types.UpsertCasePayload,
) (*types.UpsertCaseResult, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	caseNumber = strings.TrimSpace(caseNumber)
	if caseNumber == "" {
		return nil, ErrCaseNumberRequired
	}
	if payload == nil || strings.TrimSpace(payload.Summary) == "" {
		return nil, ErrCaseSummaryRequired
	}

	normalized, caseDate, chipsets, changeRequests, err := normalizeCasePayload(payload)
	if err != nil {
		return nil, err
	}
	if !validCaseFieldLengths(caseNumber, normalized, chipsets, changeRequests) {
		return nil, ErrInvalidCasePayload
	}
	markdown := buildCaseMarkdown(caseNumber, normalized, chipsets, changeRequests)
	contentHash := hashCaseContent(markdown)
	manual := &types.ManualKnowledgePayload{
		Title:   caseTitle(caseNumber, normalized.Subject),
		Content: markdown,
		Status:  types.ManualKnowledgeStatusPublish,
		Channel: types.ChannelAPI,
	}

	existing, err := s.repo.GetByTenantAndCaseNumber(ctx, tenantID, caseNumber)
	if err != nil && !errors.Is(err, repository.ErrCaseNotFound) {
		return nil, err
	}
	if existing != nil {
		if existing.KnowledgeBaseID != kbID {
			return nil, ErrCaseKnowledgeBaseMismatch
		}
		knowledge, getErr := s.knowledgeService.GetKnowledgeByID(ctx, existing.KnowledgeID)
		if getErr != nil {
			return nil, getErr
		}
		if existing.ContentHash == contentHash &&
			isCaseKnowledgePublished(knowledge.ParseStatus) {
			applyCaseValues(existing, normalized, caseDate, contentHash)
			if err := s.repo.Update(ctx, existing, nil, nil); err != nil {
				return nil, err
			}
			return caseResult(existing, types.CaseUpsertUnchanged, knowledge.ParseStatus), nil
		}
		updatedKnowledge, updateErr := s.knowledgeService.UpdateManualKnowledge(ctx, existing.KnowledgeID, manual)
		if updateErr != nil {
			return nil, updateErr
		}
		applyCaseValues(existing, normalized, caseDate, contentHash)
		if err := s.repo.Update(ctx, existing, chipsets, changeRequests); err != nil {
			return nil, err
		}
		return caseResult(existing, types.CaseUpsertUpdated, updatedKnowledge.ParseStatus), nil
	}

	// Create as draft first so no indexing task can run before the Case mapping exists.
	draft := *manual
	draft.Status = types.ManualKnowledgeStatusDraft
	knowledge, err := s.knowledgeService.CreateKnowledgeFromManual(ctx, kbID, &draft, types.ChannelAPI)
	if err != nil {
		return nil, err
	}
	item := &types.Case{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledge.ID,
		CaseNumber:      caseNumber,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	applyCaseValues(item, normalized, caseDate, contentHash)
	item.Chipsets = makeCaseChipsets(item.ID, chipsets)
	item.ChangeRequests = makeCaseChangeRequests(item.ID, changeRequests)
	if err := s.repo.Create(ctx, item); err != nil {
		_ = s.knowledgeService.DeleteKnowledge(ctx, knowledge.ID)
		return nil, err
	}
	published, err := s.knowledgeService.UpdateManualKnowledge(ctx, knowledge.ID, manual)
	if err != nil {
		return nil, err
	}
	return caseResult(item, types.CaseUpsertCreated, published.ParseStatus), nil
}

func (s *caseService) FilterKnowledgeIDs(
	ctx context.Context,
	kbIDs []string,
	filters types.CaseFilters,
) ([]string, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	from, err := parseCaseFilterDate(filters.CaseDateFrom)
	if err != nil {
		return nil, ErrInvalidCaseDateRange
	}
	to, err := parseCaseFilterDate(filters.CaseDateTo)
	if err != nil || from != nil && to != nil && from.After(*to) {
		return nil, ErrInvalidCaseDateRange
	}
	return s.repo.FilterKnowledgeIDs(ctx, tenantID, kbIDs, filters)
}

func (s *caseService) GetCasesByKnowledgeIDs(
	ctx context.Context,
	knowledgeIDs []string,
) ([]*types.Case, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrInvalidTenantID
	}
	return s.repo.ListByKnowledgeIDs(ctx, tenantID, knowledgeIDs)
}

func isCaseKnowledgePublished(parseStatus string) bool {
	switch parseStatus {
	case types.ParseStatusPending,
		types.ParseStatusProcessing,
		types.ParseStatusFinalizing,
		types.ParseStatusCompleted:
		return true
	default:
		return false
	}
}

func parseCaseFilterDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func normalizeCasePayload(payload *types.UpsertCasePayload) (*types.UpsertCasePayload, *time.Time, []string, []string, error) {
	normalized := &types.UpsertCasePayload{
		Subject:         strings.TrimSpace(payload.Subject),
		Description:     strings.TrimSpace(payload.Description),
		Summary:         strings.TrimSpace(payload.Summary),
		ProblemArea1:    strings.TrimSpace(payload.ProblemArea1),
		ProblemArea2:    strings.TrimSpace(payload.ProblemArea2),
		ProblemArea3:    strings.TrimSpace(payload.ProblemArea3),
		CaseURL:         strings.TrimSpace(payload.CaseURL),
		CaseDate:        strings.TrimSpace(payload.CaseDate),
		ExtraAttributes: payload.ExtraAttributes,
	}
	var caseDate *time.Time
	if normalized.CaseDate != "" {
		parsed, err := time.Parse("2006-01-02", normalized.CaseDate)
		if err != nil {
			return nil, nil, nil, nil, ErrInvalidCaseDate
		}
		caseDate = &parsed
	}
	return normalized, caseDate, normalizedValues(payload.Chipsets), normalizedValues(payload.ChangeRequestNumbers), nil
}

func validCaseFieldLengths(
	caseNumber string,
	payload *types.UpsertCasePayload,
	chipsets, changeRequests []string,
) bool {
	if utf8.RuneCountInString(caseNumber) > 64 ||
		utf8.RuneCountInString(payload.Subject) > 1000 ||
		utf8.RuneCountInString(payload.ProblemArea1) > 255 ||
		utf8.RuneCountInString(payload.ProblemArea2) > 255 ||
		utf8.RuneCountInString(payload.ProblemArea3) > 255 {
		return false
	}
	for _, chipset := range chipsets {
		if utf8.RuneCountInString(chipset) > 128 {
			return false
		}
	}
	for _, number := range changeRequests {
		if utf8.RuneCountInString(number) > 64 {
			return false
		}
	}
	return true
}

func normalizedValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func hashCaseContent(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

func buildCaseMarkdown(
	caseNumber string,
	payload *types.UpsertCasePayload,
	chipsets, changeRequests []string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Case Information\n\n", caseTitle(caseNumber, payload.Subject))
	writeMarkdownField(&b, "Document Type", "Support Case")
	writeMarkdownField(&b, "Case Number", caseNumber)
	writeMarkdownField(&b, "Subject", payload.Subject)
	writeMarkdownField(&b, "Chipset", strings.Join(chipsets, ", "))
	writeMarkdownField(&b, "Problem Area 1", payload.ProblemArea1)
	writeMarkdownField(&b, "Problem Area 2", payload.ProblemArea2)
	writeMarkdownField(&b, "Problem Area 3", payload.ProblemArea3)
	writeMarkdownField(&b, "Change Request Numbers", strings.Join(changeRequests, ", "))
	writeMarkdownField(&b, "Case Date", payload.CaseDate)
	writeMarkdownField(&b, "Case URL", payload.CaseURL)
	b.WriteString("\n## Case Summary\n\n")
	b.WriteString(payload.Summary)
	b.WriteString("\n")
	return b.String()
}

func writeMarkdownField(b *strings.Builder, label, value string) {
	if value != "" {
		fmt.Fprintf(b, "- %s: %s\n", label, strings.ReplaceAll(value, "\n", " "))
	}
}

func caseTitle(caseNumber, subject string) string {
	var title string
	if subject == "" {
		title = "Case " + caseNumber
	} else {
		title = fmt.Sprintf("Case %s: %s", caseNumber, subject)
	}
	// file_name = title + ".md", and file_name is VARCHAR(512)
	// so title must be at most 509 characters
	if utf8.RuneCountInString(title) > 509 {
		runes := []rune(title)
		title = string(runes[:506]) + "..."
	}
	return title
}

func applyCaseValues(item *types.Case, payload *types.UpsertCasePayload, caseDate *time.Time, contentHash string) {
	item.Subject = payload.Subject
	item.Description = payload.Description
	item.Summary = payload.Summary
	item.ProblemArea1 = payload.ProblemArea1
	item.ProblemArea2 = payload.ProblemArea2
	item.ProblemArea3 = payload.ProblemArea3
	item.CaseURL = payload.CaseURL
	item.CaseDate = caseDate
	item.ContentHash = contentHash
	item.ExtraAttributes = types.JSONMap(payload.ExtraAttributes)
	item.UpdatedAt = time.Now()
}

func makeCaseChipsets(caseID string, values []string) []types.CaseChipset {
	rows := make([]types.CaseChipset, 0, len(values))
	for _, value := range values {
		rows = append(rows, types.CaseChipset{CaseID: caseID, Chipset: value})
	}
	return rows
}

func makeCaseChangeRequests(caseID string, values []string) []types.CaseChangeRequest {
	rows := make([]types.CaseChangeRequest, 0, len(values))
	for _, value := range values {
		rows = append(rows, types.CaseChangeRequest{CaseID: caseID, ChangeRequestNumber: value})
	}
	return rows
}

func caseResult(item *types.Case, action, parseStatus string) *types.UpsertCaseResult {
	return &types.UpsertCaseResult{
		CaseNumber:      item.CaseNumber,
		KnowledgeBaseID: item.KnowledgeBaseID,
		KnowledgeID:     item.KnowledgeID,
		Action:          action,
		ParseStatus:     parseStatus,
	}
}
