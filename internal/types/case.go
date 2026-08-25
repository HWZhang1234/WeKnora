package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	CaseUpsertCreated   = "created"
	CaseUpsertUpdated   = "updated"
	CaseUpsertUnchanged = "unchanged"
)

// Case stores structured attributes for a support case represented by a Knowledge entry.
type Case struct {
	ID              string              `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64              `json:"tenant_id" gorm:"not null;uniqueIndex:uq_cases_tenant_number"`
	KnowledgeBaseID string              `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	KnowledgeID     string              `json:"knowledge_id" gorm:"type:varchar(36);not null;uniqueIndex"`
	CaseNumber      string              `json:"case_number" gorm:"type:varchar(64);not null;uniqueIndex:uq_cases_tenant_number"`
	Subject         string              `json:"subject" gorm:"type:varchar(1000)"`
	Description     string              `json:"description" gorm:"type:text"`
	Summary         string              `json:"summary" gorm:"type:text"`
	ProblemArea1    string              `json:"problem_area_1" gorm:"column:problem_area_1;type:varchar(255);index"`
	ProblemArea2    string              `json:"problem_area_2" gorm:"column:problem_area_2;type:varchar(255);index"`
	ProblemArea3    string              `json:"problem_area_3" gorm:"column:problem_area_3;type:varchar(255);index"`
	CaseURL         string              `json:"case_url" gorm:"type:text"`
	CaseDate        *time.Time          `json:"case_date,omitempty" gorm:"type:date;index"`
	ContentHash     string              `json:"content_hash" gorm:"type:varchar(64);not null"`
	ExtraAttributes JSONMap             `json:"extra_attributes,omitempty" gorm:"type:jsonb"`
	Chipsets        []CaseChipset       `json:"chipsets,omitempty" gorm:"foreignKey:CaseID;constraint:OnDelete:CASCADE"`
	ChangeRequests  []CaseChangeRequest `json:"change_requests,omitempty" gorm:"foreignKey:CaseID;constraint:OnDelete:CASCADE"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

func (c *Case) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return nil
}

// CaseChipset stores a normalized multi-valued chipset attribute.
type CaseChipset struct {
	CaseID  string `json:"-" gorm:"type:varchar(36);primaryKey"`
	Chipset string `json:"chipset" gorm:"type:varchar(128);primaryKey;index"`
}

// CaseChangeRequest stores a normalized multi-valued change request number.
type CaseChangeRequest struct {
	CaseID              string `json:"-" gorm:"type:varchar(36);primaryKey"`
	ChangeRequestNumber string `json:"change_request_number" gorm:"type:varchar(64);primaryKey;index"`
}

// UpsertCasePayload is accepted by the Case ingestion API.
type UpsertCasePayload struct {
	Subject              string         `json:"subject"`
	Description          string         `json:"description"`
	Summary              string         `json:"summary"`
	ProblemArea1         string         `json:"problem_area_1"`
	ProblemArea2         string         `json:"problem_area_2"`
	ProblemArea3         string         `json:"problem_area_3"`
	CaseURL              string         `json:"case_url"`
	CaseDate             string         `json:"case_date"`
	Chipsets             []string       `json:"chipsets"`
	ChangeRequestNumbers []string       `json:"change_request_numbers"`
	ExtraAttributes      map[string]any `json:"extra_attributes,omitempty"`
}

// UpsertCaseResult describes whether ingestion created, updated, or reused a Knowledge entry.
type UpsertCaseResult struct {
	CaseNumber      string `json:"case_number"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
	Action          string `json:"action"`
	ParseStatus     string `json:"parse_status"`
}

// CaseFilters are strict relational filters applied before semantic search.
type CaseFilters struct {
	CaseNumber           string   `json:"case_number,omitempty"`
	Chipsets             []string `json:"chipsets,omitempty"`
	ProblemArea1         []string `json:"problem_area_1,omitempty"`
	ProblemArea2         []string `json:"problem_area_2,omitempty"`
	ProblemArea3         []string `json:"problem_area_3,omitempty"`
	ChangeRequestNumbers []string `json:"change_request_numbers,omitempty"`
	CaseDateFrom         string   `json:"case_date_from,omitempty"`
	CaseDateTo           string   `json:"case_date_to,omitempty"`
}

func (f CaseFilters) Empty() bool {
	return f.CaseNumber == "" && len(f.Chipsets) == 0 && len(f.ProblemArea1) == 0 &&
		len(f.ProblemArea2) == 0 && len(f.ProblemArea3) == 0 && len(f.ChangeRequestNumbers) == 0 &&
		f.CaseDateFrom == "" && f.CaseDateTo == ""
}
