package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateNotificationChannel(c *gin.Context) {
	projectID := c.Param("projectId")

	var req models.CreateNotificationChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	configBytes, err := json.Marshal(req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid config"})
		return
	}

	ch := &db.NotificationChannel{
		ProjectID: projectID,
		Name:      req.Name,
		Type:      req.Type,
		Config:    configBytes,
		Events:    req.Events,
		Enabled:   true,
	}

	if err := h.DB.CreateNotificationChannel(ch); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, ch)
}

func (h *Handler) ListNotificationChannels(c *gin.Context) {
	projectID := c.Param("projectId")

	channels, err := h.DB.ListNotificationChannels(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if channels == nil {
		channels = []db.NotificationChannel{}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: channels, Total: len(channels)})
}

func (h *Handler) UpdateNotificationChannel(c *gin.Context) {
	channelID := c.Param("channelId")
	projectID := c.Param("projectId")

	existing, err := h.DB.GetNotificationChannel(channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if existing == nil || existing.ProjectID != projectID {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "channel not found"})
		return
	}

	var req models.UpdateNotificationChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Config != nil {
		configBytes, err := json.Marshal(req.Config)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid config"})
			return
		}
		existing.Config = configBytes
	}
	if req.Events != nil {
		existing.Events = req.Events
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := h.DB.UpdateNotificationChannel(existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, existing)
}

func (h *Handler) DeleteNotificationChannel(c *gin.Context) {
	channelID := c.Param("channelId")
	projectID := c.Param("projectId")

	existing, err := h.DB.GetNotificationChannel(channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if existing == nil || existing.ProjectID != projectID {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "channel not found"})
		return
	}

	if err := h.DB.DeleteNotificationChannel(channelID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) TestNotificationChannel(c *gin.Context) {
	channelID := c.Param("channelId")
	projectID := c.Param("projectId")

	existing, err := h.DB.GetNotificationChannel(channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if existing == nil || existing.ProjectID != projectID {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "channel not found"})
		return
	}

	if err := h.Notifier.SendTest(c.Request.Context(), *existing); err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "sent"})
}

func (h *Handler) ListNotificationHistory(c *gin.Context) {
	projectID := c.Param("projectId")

	limit := 50
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	items, err := h.DB.ListNotificationHistory(projectID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if items == nil {
		items = []db.NotificationHistory{}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}
