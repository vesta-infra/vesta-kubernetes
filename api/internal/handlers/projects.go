package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

const vestaSystemNS = "vesta-system"

func (h *Handler) CreateProject(c *gin.Context) {
	var req models.CreateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	spec := map[string]interface{}{
		"team": req.Team,
	}
	if req.DisplayName != "" {
		spec["displayName"] = req.DisplayName
	}
	if req.Labels != nil {
		spec["labels"] = req.Labels
	}
	if req.Annotations != nil {
		spec["annotations"] = req.Annotations
	}
	if req.DefaultGit != nil {
		spec["defaultGit"] = req.DefaultGit
	}
	if req.DefaultBuild != nil {
		spec["defaultBuild"] = req.DefaultBuild
	}
	if req.DefaultImage != nil {
		spec["defaultImage"] = req.DefaultImage
	}

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaProject",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": vestaSystemNS,
		},
		"spec": spec,
	}

	result, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        result.GetName(),
		"name":      result.GetName(),
		"spec":      spec,
		"createdAt": result.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	})

	h.auditLog(c, "create_project", "project", result.GetName(), result.GetName(), result.GetName(), "", nil)
}

func (h *Handler) ListProjects(c *gin.Context) {
	team := c.Query("team")
	labelSelector := ""
	if team != "" {
		labelSelector = "kubernetes.getvesta.sh/team=" + team
	}

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		status, _, _ := unstructuredNestedMap(item.Object, "status")
		items[i] = map[string]interface{}{
			"id":        item.GetName(),
			"name":      item.GetName(),
			"spec":      spec,
			"status":    status,
			"createdAt": item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
		}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) GetProject(c *gin.Context) {
	projectID := c.Param("projectId")

	result, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(result.Object, "spec")
	status, _, _ := unstructuredNestedMap(result.Object, "status")

	// Get environments from project spec
	envs := []interface{}{}
	if spec != nil {
		if e, ok := spec["environments"].([]interface{}); ok {
			envs = e
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":           result.GetName(),
		"name":         result.GetName(),
		"spec":         spec,
		"status":       status,
		"environments": envs,
		"createdAt":    result.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	})
}

func (h *Handler) UpdateProject(c *gin.Context) {
	projectID := c.Param("projectId")

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	for k, v := range patch {
		spec[k] = v
	}
	existing.Object["spec"] = spec

	result, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, existing)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": result.GetName(), "name": result.GetName()})

	h.auditLog(c, "update_project", "project", projectID, projectID, projectID, "", nil)
}

func (h *Handler) DeleteProject(c *gin.Context) {
	projectID := c.Param("projectId")

	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "delete_project", "project", projectID, projectID, projectID, "", nil)

	c.Status(http.StatusNoContent)
}
