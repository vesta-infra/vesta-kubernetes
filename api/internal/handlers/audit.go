package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) ListAuditLogs(c *gin.Context) {
	filter := db.AuditLogFilter{
		ProjectID:    c.Query("projectId"),
		AppID:        c.Query("appId"),
		Action:       c.Query("action"),
		UserID:       c.Query("userId"),
		ResourceType: c.Query("resourceType"),
	}

	if v := c.Query("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}
	if v := c.Query("offset"); v != "" {
		filter.Offset, _ = strconv.Atoi(v)
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.From = &t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.To = &t
		}
	}

	entries, total, err := h.DB.ListAuditLogs(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: entries, Total: total})
}

func (h *Handler) GetActivityFeed(c *gin.Context) {
	filter := db.AuditLogFilter{
		ProjectID: c.Query("projectId"),
	}

	if v := c.Query("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	} else {
		filter.Limit = 30
	}
	if v := c.Query("offset"); v != "" {
		filter.Offset, _ = strconv.Atoi(v)
	}

	entries, total, err := h.DB.ListAuditLogs(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: entries, Total: total})
}

func (h *Handler) ListWebhookDeliveries(c *gin.Context) {
	filter := db.WebhookDeliveryFilter{
		Provider:   c.Query("provider"),
		Status:     c.Query("status"),
		Repository: c.Query("repository"),
	}

	if v := c.Query("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}
	if v := c.Query("offset"); v != "" {
		filter.Offset, _ = strconv.Atoi(v)
	}

	entries, total, err := h.DB.ListWebhookDeliveries(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: entries, Total: total})
}

// auditLog is a helper to record an audit log entry from a handler context.
func (h *Handler) auditLog(c *gin.Context, action, resourceType, resourceID, resourceName, projectID, environment string, metadata map[string]interface{}) {
	userId := c.GetString("userId")
	username := ""
	if userId != "" {
		if user, err := h.DB.GetUserByID(c.Request.Context(), userId); err == nil {
			username = user.Username
		}
	}
	entry := db.AuditLogEntry{
		UserID:       userId,
		Username:     username,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		ProjectID:    projectID,
		Environment:  environment,
		Metadata:     metadata,
		IPAddress:    c.ClientIP(),
		AuthMethod:   c.GetString("authType"),
	}
	// Fire and forget — don't block the request
	go h.DB.InsertAuditLog(c.Request.Context(), entry)
}
