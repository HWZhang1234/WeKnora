package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrCaseNotFound = errors.New("case not found")

type caseRepository struct {
	db *gorm.DB
}

func NewCaseRepository(db *gorm.DB) interfaces.CaseRepository {
	return &caseRepository{db: db}
}

func (r *caseRepository) GetByTenantAndCaseNumber(
	ctx context.Context,
	tenantID uint64,
	caseNumber string,
) (*types.Case, error) {
	var item types.Case
	err := r.db.WithContext(ctx).
		Preload("Chipsets").
		Preload("ChangeRequests").
		Where("tenant_id = ? AND case_number = ?", tenantID, caseNumber).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *caseRepository) Create(ctx context.Context, item *types.Case) error {
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *caseRepository) ExistsByKnowledgeID(ctx context.Context, knowledgeID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.Case{}).
		Where("knowledge_id = ?", knowledgeID).
		Count(&count).Error
	return count > 0, err
}

func (r *caseRepository) Update(
	ctx context.Context,
	item *types.Case,
	chipsets, changeRequests []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Chipsets", "ChangeRequests").Save(item).Error; err != nil {
			return err
		}
		if chipsets == nil && changeRequests == nil {
			return nil
		}
		if err := tx.Where("case_id = ?", item.ID).Delete(&types.CaseChipset{}).Error; err != nil {
			return err
		}
		if err := tx.Where("case_id = ?", item.ID).Delete(&types.CaseChangeRequest{}).Error; err != nil {
			return err
		}
		if len(chipsets) > 0 {
			rows := make([]types.CaseChipset, 0, len(chipsets))
			for _, chipset := range chipsets {
				rows = append(rows, types.CaseChipset{CaseID: item.ID, Chipset: chipset})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		if len(changeRequests) > 0 {
			rows := make([]types.CaseChangeRequest, 0, len(changeRequests))
			for _, number := range changeRequests {
				rows = append(rows, types.CaseChangeRequest{CaseID: item.ID, ChangeRequestNumber: number})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *caseRepository) FilterKnowledgeIDs(
	ctx context.Context,
	tenantID uint64,
	kbIDs []string,
	filters types.CaseFilters,
) ([]string, error) {
	if len(kbIDs) == 0 {
		return []string{}, nil
	}

	query := r.db.WithContext(ctx).Model(&types.Case{}).
		Distinct("cases.knowledge_id").
		Where("cases.tenant_id = ?", tenantID).
		Where("cases.knowledge_base_id IN ?", kbIDs)
	if value := strings.TrimSpace(filters.CaseNumber); value != "" {
		query = query.Where("cases.case_number = ?", value)
	}
	if values := cleanFilterValues(filters.ProblemArea1); len(values) > 0 {
		query = query.Where("cases.problem_area_1 IN ?", values)
	}
	if values := cleanFilterValues(filters.ProblemArea2); len(values) > 0 {
		query = query.Where("cases.problem_area_2 IN ?", values)
	}
	if values := cleanFilterValues(filters.ProblemArea3); len(values) > 0 {
		query = query.Where("cases.problem_area_3 IN ?", values)
	}
	if values := cleanFilterValues(filters.Chipsets); len(values) > 0 {
		query = query.Where(`EXISTS (
			SELECT 1 FROM case_chipsets cc
			WHERE cc.case_id = cases.id AND cc.chipset IN ?
		)`, values)
	}
	if values := cleanFilterValues(filters.ChangeRequestNumbers); len(values) > 0 {
		query = query.Where(`EXISTS (
			SELECT 1 FROM case_change_requests ccr
			WHERE ccr.case_id = cases.id AND ccr.change_request_number IN ?
		)`, values)
	}
	if value := strings.TrimSpace(filters.CaseDateFrom); value != "" {
		query = query.Where("cases.case_date >= ?", value)
	}
	if value := strings.TrimSpace(filters.CaseDateTo); value != "" {
		query = query.Where("cases.case_date <= ?", value)
	}

	var ids []string
	if err := query.Pluck("cases.knowledge_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func cleanFilterValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
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
	return result
}
