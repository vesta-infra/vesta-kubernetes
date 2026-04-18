package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) ListPodFiles(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Query("environment")
	pod := c.Query("pod")
	container := c.Query("container")
	path := c.Query("path")

	if env == "" || pod == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment and pod are required"})
		return
	}
	if path == "" {
		path = "/"
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := project + "-" + env

	files, err := h.K8s.ListFiles(c.Request.Context(), namespace, pod, container, path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": files, "path": path})
}

func (h *Handler) ReadPodFile(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Query("environment")
	pod := c.Query("pod")
	container := c.Query("container")
	path := c.Query("path")

	if env == "" || pod == "" || path == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment, pod, and path are required"})
		return
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := project + "-" + env

	content, err := h.K8s.ReadFile(c.Request.Context(), namespace, pod, container, path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"path": path, "content": content})
}

func (h *Handler) WritePodFile(c *gin.Context) {
	appID := c.Param("appId")

	var req struct {
		Environment string `json:"environment" binding:"required"`
		Pod         string `json:"pod" binding:"required"`
		Container   string `json:"container"`
		Path        string `json:"path" binding:"required"`
		Content     string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Validate content size (max 1MB)
	if len(req.Content) > 1024*1024 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "file content too large (max 1MB)"})
		return
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := project + "-" + req.Environment

	if err := h.K8s.WriteFile(c.Request.Context(), namespace, req.Pod, req.Container, req.Path, req.Content); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "file written", "path": req.Path})
}
