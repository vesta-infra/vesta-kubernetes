package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ListTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"items": []interface{}{}, "total": 0})
}

func (h *Handler) DeployTemplate(c *gin.Context) {
	templateId := c.Param("id")
	var req struct {
		Project      string                 `json:"project" binding:"required"`
		Environments []string               `json:"environments,omitempty"`
		Name         string                 `json:"name,omitempty"`
		Overrides    map[string]interface{} `json:"overrides,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":           req.Name,
		"template":     templateId,
		"project":      req.Project,
		"environments": req.Environments,
	})
}
