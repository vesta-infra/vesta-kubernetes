package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateScheduledDeployment(c *gin.Context) {
	projectID := c.Param("projectId")
	var req struct {
		AppID       string    `json:"appId" binding:"required"`
		Environment string    `json:"environment"`
		Image       string    `json:"image"`
		Tag         string    `json:"tag"`
		ScheduledAt time.Time `json:"scheduledAt" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if req.ScheduledAt.Before(time.Now()) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "scheduled time must be in the future"})
		return
	}

	userID, _ := c.Get("userId")

	sd := &db.ScheduledDeployment{
		AppID:       req.AppID,
		ProjectID:   projectID,
		Environment: req.Environment,
		Image:       req.Image,
		Tag:         req.Tag,
		ScheduledAt: req.ScheduledAt,
		CreatedBy:   userID.(string),
	}

	if err := h.DB.CreateScheduledDeployment(sd); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	go auditLog(h.DB, userID.(string), "schedule_deploy", "app", req.AppID, projectID, map[string]string{
		"environment": req.Environment,
		"image":       req.Image,
		"tag":         req.Tag,
		"scheduledAt": req.ScheduledAt.Format(time.RFC3339),
	})

	c.JSON(http.StatusCreated, sd)
}

func (h *Handler) ListScheduledDeployments(c *gin.Context) {
	projectID := c.Param("projectId")

	items, err := h.DB.ListScheduledDeployments(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if items == nil {
		items = []db.ScheduledDeployment{}
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

func (h *Handler) CancelScheduledDeployment(c *gin.Context) {
	id := c.Param("deploymentId")

	if err := h.DB.CancelScheduledDeployment(id); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	userID, _ := c.Get("userId")
	go auditLog(h.DB, userID.(string), "cancel_scheduled_deploy", "scheduled_deployment", id, c.Param("projectId"), nil)

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}
