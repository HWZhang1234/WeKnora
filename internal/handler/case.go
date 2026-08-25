package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// CaseHandler handles structured Case ingestion.
type CaseHandler struct {
	caseService interfaces.CaseService
}

func NewCaseHandler(caseService interfaces.CaseService) *CaseHandler {
	return &CaseHandler{caseService: caseService}
}

// UpsertCase godoc
// @Summary      创建或更新 Case 知识
// @Description  按 Case Number 幂等写入结构化 Case 属性，并通过现有 Knowledge/Chunk 流程异步建立检索索引。Comments 不应放入请求体。
// @Tags         Case 管理
// @Accept       json
// @Produce      json
// @Param        id           path      string                  true  "知识库 ID"
// @Param        case_number  path      string                  true  "Case Number"
// @Param        request      body      types.UpsertCasePayload true  "Case 数据；summary 必填，case_date 使用 YYYY-MM-DD"
// @Success      200          {object}  map[string]interface{}  "action 为 created、updated 或 unchanged"
// @Failure      400          {object}  werrors.AppError        "请求内容无效"
// @Failure      401          {object}  werrors.AppError        "未认证"
// @Failure      403          {object}  werrors.AppError        "无知识库写权限"
// @Failure      404          {object}  werrors.AppError        "知识库不存在"
// @Failure      409          {object}  werrors.AppError        "Case 已属于其他知识库"
// @Failure      500          {object}  werrors.AppError        "服务端错误"
// @Failure      503          {object}  werrors.AppError        "权限依赖服务暂时不可用"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/cases/{case_number} [put]
func (h *CaseHandler) UpsertCase(c *gin.Context) {
	kbID := strings.TrimSpace(c.Param("id"))
	caseNumber := strings.TrimSpace(c.Param("case_number"))
	if kbID == "" || caseNumber == "" {
		_ = c.Error(werrors.NewBadRequestError("knowledge base ID and case number are required"))
		return
	}

	var payload types.UpsertCasePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		_ = c.Error(werrors.NewBadRequestError("invalid Case payload").WithDetails(err.Error()))
		return
	}
	result, err := h.caseService.UpsertCase(c.Request.Context(), kbID, caseNumber, &payload)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCaseNumberRequired),
			errors.Is(err, service.ErrCaseSummaryRequired),
			errors.Is(err, service.ErrInvalidCaseDate),
			errors.Is(err, service.ErrInvalidCasePayload):
			_ = c.Error(werrors.NewBadRequestError(err.Error()))
		case errors.Is(err, service.ErrCaseKnowledgeBaseMismatch):
			_ = c.Error(werrors.NewConflictError(err.Error()))
		default:
			if appErr, ok := werrors.IsAppError(err); ok {
				_ = c.Error(appErr)
			} else {
				_ = c.Error(werrors.NewInternalServerError(err.Error()))
			}
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
