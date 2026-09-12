package handlers

import (
	"encoding/json"
	"log"
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

	// Create VestaEnvironment CR for webhook auto-deploy matching
	envCR := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaEnvironment",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": vestaSystemNS,
			"labels": map[string]interface{}{
				"kubernetes.getvesta.sh/project":     projectID,
				"kubernetes.getvesta.sh/environment": req.Name,
			},
		},
		"spec": map[string]interface{}{
			"project":         projectID,
			"displayName":     req.DisplayName,
			"order":           req.Order,
			"branch":          req.Branch,
			"autoDeploy":      req.AutoDeploy,
			"requireApproval": req.RequireApproval,
			"autoDeployPRs":   req.AutoDeployPRs,
		},
	}
	if _, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, envCR); err != nil {
		log.Printf("[env] failed to create VestaEnvironment CR %s: %v", req.Name, err)
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

	h.auditLog(c, "create_env", "environment", req.Name, req.Name, projectID, req.Name, nil)
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

	// Delete VestaEnvironment CR
	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, envName); err != nil {
		log.Printf("[env] failed to delete VestaEnvironment CR %s: %v", envName, err)
	}

	h.auditLog(c, "delete_env", "environment", envName, envName, projectID, envName, nil)

	c.Status(http.StatusNoContent)
}

func (h *Handler) UpdateEnvironment(c *gin.Context) {
	projectID := c.Param("projectId")
	envName := c.Param("env")

	var req struct {
		Branch          *string `json:"branch"`
		AutoDeploy      *bool   `json:"autoDeploy"`
		RequireApproval *bool   `json:"requireApproval"`
		AutoDeployPRs   *bool   `json:"autoDeployPRs"`
		DisplayName     *string `json:"displayName"`
		Order           *int    `json:"order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Update project spec entry
	project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(project.Object, "spec")
	envs, _ := spec["environments"].([]interface{})

	found := false
	for i, e := range envs {
		env, ok := e.(map[string]interface{})
		if !ok || env["name"] != envName {
			continue
		}
		found = true
		if req.Branch != nil {
			env["branch"] = *req.Branch
		}
		if req.AutoDeploy != nil {
			env["autoDeploy"] = *req.AutoDeploy
		}
		if req.RequireApproval != nil {
			env["requireApproval"] = *req.RequireApproval
		}
		if req.AutoDeployPRs != nil {
			env["autoDeployPRs"] = *req.AutoDeployPRs
		}
		if req.DisplayName != nil {
			env["displayName"] = *req.DisplayName
		}
		if req.Order != nil {
			env["order"] = *req.Order
		}
		envs[i] = env
		break
	}

	if !found {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "environment not found"})
		return
	}

	spec["environments"] = envs
	project.Object["spec"] = spec
	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, project); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// Update VestaEnvironment CR
	envCR, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, envName)
	if err == nil {
		crSpec, _, _ := unstructuredNestedMap(envCR.Object, "spec")
		if crSpec == nil {
			crSpec = map[string]interface{}{}
		}
		if req.Branch != nil {
			crSpec["branch"] = *req.Branch
		}
		if req.AutoDeploy != nil {
			crSpec["autoDeploy"] = *req.AutoDeploy
		}
		if req.RequireApproval != nil {
			crSpec["requireApproval"] = *req.RequireApproval
		}
		if req.AutoDeployPRs != nil {
			crSpec["autoDeployPRs"] = *req.AutoDeployPRs
		}
		if req.DisplayName != nil {
			crSpec["displayName"] = *req.DisplayName
		}
		if req.Order != nil {
			crSpec["order"] = *req.Order
		}
		envCR.Object["spec"] = crSpec
		if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, envCR); err != nil {
			log.Printf("[env] failed to update VestaEnvironment CR %s: %v", envName, err)
		}
	} else {
		log.Printf("[env] VestaEnvironment CR %s not found, creating...", envName)
		// Create if missing (for environments created before this feature)
		envCRObj := map[string]interface{}{
			"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
			"kind":       "VestaEnvironment",
			"metadata": map[string]interface{}{
				"name":      envName,
				"namespace": vestaSystemNS,
				"labels": map[string]interface{}{
					"kubernetes.getvesta.sh/project":     projectID,
					"kubernetes.getvesta.sh/environment": envName,
				},
			},
			"spec": map[string]interface{}{
				"project": projectID,
			},
		}
		crSpec := envCRObj["spec"].(map[string]interface{})
		if req.Branch != nil {
			crSpec["branch"] = *req.Branch
		}
		if req.AutoDeploy != nil {
			crSpec["autoDeploy"] = *req.AutoDeploy
		}
		if _, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, envCRObj); err != nil {
			log.Printf("[env] failed to create VestaEnvironment CR %s: %v", envName, err)
		}
	}

	// Return updated env
	for _, e := range envs {
		env, ok := e.(map[string]interface{})
		if ok && env["name"] == envName {
			c.JSON(http.StatusOK, env)
			break
		}
	}

	h.auditLog(c, "update_env", "environment", envName, envName, projectID, envName,
		map[string]interface{}{"branch": req.Branch, "autoDeploy": req.AutoDeploy})
}

func (h *Handler) CloneEnvironment(c *gin.Context) {
	projectID := c.Param("projectId")
	sourceEnv := c.Param("env")

	var req struct {
		Name   string `json:"name" binding:"required"`
		Branch string `json:"branch,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(project.Object, "spec")
	envs, _ := spec["environments"].([]interface{})

	// Find source environment config
	var sourceConfig map[string]interface{}
	for _, e := range envs {
		env, ok := e.(map[string]interface{})
		if ok && env["name"] == sourceEnv {
			sourceConfig = env
			break
		}
	}
	if sourceConfig == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "source environment not found"})
		return
	}

	// Check target doesn't exist
	for _, e := range envs {
		env, ok := e.(map[string]interface{})
		if ok && env["name"] == req.Name {
			c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "environment already exists"})
			return
		}
	}

	// Clone environment config
	newEnv := make(map[string]interface{})
	for k, v := range sourceConfig {
		newEnv[k] = v
	}
	newEnv["name"] = req.Name
	if req.Branch != "" {
		newEnv["branch"] = req.Branch
	}

	envs = append(envs, newEnv)
	spec["environments"] = envs
	project.Object["spec"] = spec

	_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, project)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// Add the new environment to all apps that have the source environment
	appsUpdated := 0
	apps, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS,
		"kubernetes.getvesta.sh/project="+projectID)
	if err == nil {
		for _, app := range apps.Items {
			appEnvs, _, _ := unstructuredNestedSlice(app.Object, "spec", "environments")
			hasSource := false
			var sourceEnvConfig map[string]interface{}
			for _, ae := range appEnvs {
				envMap, ok := ae.(map[string]interface{})
				if !ok {
					continue
				}
				if name, _ := envMap["name"].(string); name == sourceEnv {
					hasSource = true
					sourceEnvConfig = envMap
					break
				}
			}
			if !hasSource {
				continue
			}

			// Clone per-env config
			newAppEnv := make(map[string]interface{})
			if sourceEnvConfig != nil {
				for k, v := range sourceEnvConfig {
					newAppEnv[k] = v
				}
			}
			newAppEnv["name"] = req.Name

			appEnvs = append(appEnvs, newAppEnv)
			patchData := map[string]interface{}{
				"spec": map[string]interface{}{
					"environments": appEnvs,
				},
			}
			patchBytes, _ := json.Marshal(patchData)
			_, patchErr := h.K8s.PatchResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, app.GetName(), patchBytes)
			if patchErr == nil {
				appsUpdated++
			}
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"name":        req.Name,
		"clonedFrom":  sourceEnv,
		"appsUpdated": appsUpdated,
	})

	h.auditLog(c, "clone_env", "environment", req.Name, req.Name, projectID, req.Name,
		map[string]interface{}{"clonedFrom": sourceEnv, "appsUpdated": appsUpdated})
}
