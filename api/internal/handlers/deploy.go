package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) DeployApp(c *gin.Context) {
	appId := c.Param("appId")

	var req models.DeployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	project := getNestedString(spec, "project")

	// Validate the environment exists on the app
	if !h.appHasEnvironment(existing.Object, req.Environment) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("environment %q not found on app %s", req.Environment, appId),
		})
		return
	}

	targetNS := fmt.Sprintf("%s-%s", project, req.Environment)

	deployType := req.Type
	if deployType == "" && req.Tag != "" {
		deployType = "image"
	}

	deployId := fmt.Sprintf("deploy-%s-%s-%d", appId, req.Environment, time.Now().Unix())
	triggeredBy := "api-token"
	if uid := c.GetString("userId"); uid != "" {
		triggeredBy = fmt.Sprintf("user:%s", uid)
	}
	now := models.NowRFC3339()

	switch deployType {
	case "image", "":
		if req.Tag == "" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "tag is required for image deployments"})
			return
		}

		imageSpec, ok, _ := unstructuredNestedMap(spec, "image")
		if !ok || imageSpec == nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code:    400,
				Message: "app has no image configuration; set spec.image.repository first via PUT /apps/:appId",
			})
			return
		}

		repo, _ := imageSpec["repository"].(string)
		targetImage := fmt.Sprintf("%s:%s", repo, req.Tag)

		// Patch the K8s Deployment in the target environment namespace
		patch := map[string]interface{}{
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []map[string]interface{}{
							{"name": appId, "image": targetImage},
						},
					},
				},
			},
		}
		patchBytes, _ := json.Marshal(patch)
		_, err := h.K8s.PatchResource(c.Request.Context(), k8s.DeploymentGVR, targetNS, appId, patchBytes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to patch deployment in %s: %v", targetNS, err)})
			return
		}

		c.JSON(http.StatusAccepted, models.DeployResponse{
			ID:          deployId,
			AppID:       appId,
			Status:      "deploying",
			Image:       targetImage,
			TriggeredBy: triggeredBy,
			TriggeredAt: now,
			StatusURL:   fmt.Sprintf("/api/v1/apps/%s/deployments/%s", appId, deployId),
		})

	case "redeploy":
		// Rolling restart of the deployment in the target namespace
		patch := map[string]interface{}{
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							"kubernetes.getvesta.sh/restartedAt": now,
						},
					},
				},
			},
		}
		patchBytes, _ := json.Marshal(patch)
		_, err := h.K8s.PatchResource(c.Request.Context(), k8s.DeploymentGVR, targetNS, appId, patchBytes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to restart deployment in %s: %v", targetNS, err)})
			return
		}

		c.JSON(http.StatusAccepted, models.DeployResponse{
			ID:          deployId,
			AppID:       appId,
			Status:      "deploying",
			TriggeredBy: triggeredBy,
			TriggeredAt: now,
			StatusURL:   fmt.Sprintf("/api/v1/apps/%s/deployments/%s", appId, deployId),
		})

	default:
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: fmt.Sprintf("unknown deploy type: %s", deployType)})
	}
}

func (h *Handler) RollbackApp(c *gin.Context) {
	appId := c.Param("appId")
	var req models.RollbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	project := getNestedString(spec, "project")

	if !h.appHasEnvironment(existing.Object, req.Environment) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("environment %q not found on app %s", req.Environment, appId),
		})
		return
	}

	targetNS := fmt.Sprintf("%s-%s", project, req.Environment)

	status, _, _ := unstructuredNestedMap(existing.Object, "status")
	history, _ := status["deploymentHistory"].([]interface{})

	var targetImage string
	for _, entry := range history {
		record, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		ver, _ := record["version"].(float64)
		if int(ver) == req.Version {
			targetImage, _ = record["image"].(string)
			break
		}
	}

	if targetImage == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("deployment version %d not found in history", req.Version),
		})
		return
	}

	// Patch the K8s Deployment in the target environment namespace
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []map[string]interface{}{
						{"name": appId, "image": targetImage},
					},
				},
			},
		},
	}
	patchBytes, _ := json.Marshal(patch)
	_, err = h.K8s.PatchResource(c.Request.Context(), k8s.DeploymentGVR, targetNS, appId, patchBytes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to rollback deployment in %s: %v", targetNS, err)})
		return
	}

	now := models.NowRFC3339()
	c.JSON(http.StatusAccepted, models.DeployResponse{
		ID:          fmt.Sprintf("rollback-%s-%s-%d", appId, req.Environment, time.Now().Unix()),
		AppID:       appId,
		Status:      "deploying",
		Version:     req.Version,
		Image:       targetImage,
		TriggeredBy: c.GetString("userId"),
		TriggeredAt: now,
		StatusURL:   fmt.Sprintf("/api/v1/apps/%s/deployments/latest", appId),
	})
}

func (h *Handler) ListDeployments(c *gin.Context) {
	appId := c.Param("appId")

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	status, _, _ := unstructuredNestedMap(existing.Object, "status")
	history, _ := status["deploymentHistory"].([]interface{})
	if history == nil {
		history = []interface{}{}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: history, Total: len(history)})
}

func (h *Handler) RestartApp(c *gin.Context) {
	appId := c.Param("appId")

	var req struct {
		Environment string `json:"environment" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	project := getNestedString(spec, "project")

	if !h.appHasEnvironment(existing.Object, req.Environment) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("environment %q not found on app %s", req.Environment, appId),
		})
		return
	}

	targetNS := fmt.Sprintf("%s-%s", project, req.Environment)
	now := models.NowRFC3339()

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"kubernetes.getvesta.sh/restartedAt": now,
					},
				},
			},
		},
	}
	patchBytes, _ := json.Marshal(patch)
	_, err = h.K8s.PatchResource(c.Request.Context(), k8s.DeploymentGVR, targetNS, appId, patchBytes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to restart deployment in %s: %v", targetNS, err)})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "restarting", "environment": req.Environment})
}

func (h *Handler) ScaleApp(c *gin.Context) {
	appId := c.Param("appId")
	var req models.ScaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"scaling": map[string]interface{}{
				"replicas": req.Replicas,
			},
		},
	}
	patchBytes, _ := json.Marshal(patch)
	_, err := h.K8s.PatchResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId, patchBytes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"replicas": req.Replicas})
}

func extractTag(image string) string {
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			return image[i+1:]
		}
	}
	return "latest"
}

func (h *Handler) appHasEnvironment(obj map[string]interface{}, envName string) bool {
	envs, ok, _ := unstructuredNestedSlice(obj, "spec", "environments")
	if !ok {
		return false
	}
	for _, e := range envs {
		switch v := e.(type) {
		case map[string]interface{}:
			if name, _ := v["name"].(string); name == envName {
				return true
			}
		case string:
			if v == envName {
				return true
			}
		}
	}
	return false
}
