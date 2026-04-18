package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateAlertRule(c *gin.Context) {
	projectID := c.Param("projectId")

	var req struct {
		AppID           string  `json:"appId"`
		Name            string  `json:"name" binding:"required"`
		Metric          string  `json:"metric" binding:"required"`
		Operator        string  `json:"operator" binding:"required"`
		Threshold       float64 `json:"threshold" binding:"required"`
		DurationSeconds int     `json:"durationSeconds"`
		Environment     string  `json:"environment"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	validMetrics := map[string]bool{"cpu": true, "memory": true, "restarts": true, "replicas": true, "http_error_rate": true}
	if !validMetrics[req.Metric] {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid metric"})
		return
	}

	validOps := map[string]bool{">": true, "<": true, ">=": true, "<=": true, "=": true}
	if !validOps[req.Operator] {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid operator"})
		return
	}

	if req.DurationSeconds == 0 {
		req.DurationSeconds = 60
	}

	rule, err := h.DB.CreateAlertRule(c.Request.Context(), &db.AlertRule{
		ProjectID:       projectID,
		AppID:           req.AppID,
		Name:            req.Name,
		Metric:          req.Metric,
		Operator:        req.Operator,
		Threshold:       req.Threshold,
		DurationSeconds: req.DurationSeconds,
		Environment:     req.Environment,
		Enabled:         true,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, rule)
}

func (h *Handler) ListAlertRules(c *gin.Context) {
	projectID := c.Param("projectId")

	rules, err := h.DB.ListAlertRules(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if rules == nil {
		rules = []db.AlertRule{}
	}

	c.JSON(http.StatusOK, gin.H{"items": rules, "total": len(rules)})
}

func (h *Handler) UpdateAlertRule(c *gin.Context) {
	ruleID := c.Param("ruleId")

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if err := h.DB.UpdateAlertRule(c.Request.Context(), ruleID, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": ruleID, "enabled": req.Enabled})
}

func (h *Handler) DeleteAlertRule(c *gin.Context) {
	ruleID := c.Param("ruleId")

	if err := h.DB.DeleteAlertRule(c.Request.Context(), ruleID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}
