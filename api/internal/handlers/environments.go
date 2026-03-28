package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateEnvironment(c *gin.Context) {
	projectID := c.Param("projectId")
	var req models.CreateEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Get the project
	project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(project.Object, "spec")
	if spec == nil {
		spec = map[string]interface{}{}
	}

	// Get existing environments
	envs, _ := spec["environments"].([]interface{})
	if envs == nil {
		envs = []interface{}{}
	}

	// Check if environment already exists
	for _, e := range envs {
		env, ok := e.(map[string]interface{})
		if ok && env["name"] == req.Name {
			c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "environment already exists"})
			return
		}
	}

	// Create new environment entry
	newEnv := map[string]interface{}{
		"name": req.Name,
	}
	if req.DisplayName != "" {
		newEnv["displayName"] = req.DisplayName
	}
	if req.Branch != "" {
		newEnv["branch"] = req.Branch
	}
	if req.Order > 0 {
		newEnv["order"] = req.Order
	}
	if req.AutoDeploy {
		newEnv["autoDeploy"] = true
	}
	if req.RequireApproval {
		newEnv["requireApproval"] = true
	}
	if req.AutoDeployPRs {
		newEnv["autoDeployPRs"] = true
	}

	// Append and update
	envs = append(envs, newEnv)
	spec["environments"] = envs
	project.Object["spec"] = spec

	_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, project)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"name":            req.Name,
		"displayName":     req.DisplayName,
		"branch":          req.Branch,
		"order":           req.Order,
		"autoDeploy":      req.AutoDeploy,
		"requireApproval": req.RequireApproval,
		"autoDeployPRs":   req.AutoDeployPRs,
	})
}

func (h *Handler) ListEnvironments(c *gin.Context) {
	projectID := c.Param("projectId")

	project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(project.Object, "spec")
	envs, _ := spec["environments"].([]interface{})

	items := make([]map[string]interface{}, len(envs))
	for i, e := range envs {
		env, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		items[i] = map[string]interface{}{
			"name":            env["name"],
			"displayName":     env["displayName"],
			"branch":          env["branch"],
			"order":           env["order"],
			"autoDeploy":      env["autoDeploy"],
			"requireApproval": env["requireApproval"],
			"autoDeployPRs":   env["autoDeployPRs"],
		}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) DeleteEnvironment(c *gin.Context) {
	projectID := c.Param("projectId")
	envName := c.Param("env")

	project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(project.Object, "spec")
	envs, _ := spec["environments"].([]interface{})

	found := false
	newEnvs := []interface{}{}
	for _, e := range envs {
		env, ok := e.(map[string]interface{})
		if ok && env["name"] == envName {
			found = true
			continue
		}
		newEnvs = append(newEnvs, e)
	}

	if !found {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "environment not found"})
		return
	}

	spec["environments"] = newEnvs
	project.Object["spec"] = spec

	_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, project)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}
