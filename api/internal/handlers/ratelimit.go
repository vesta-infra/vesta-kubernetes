package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// Rate limit annotation keys for common ingress controllers
var rateLimitAnnotationKeys = []string{
	// nginx
	"nginx.ingress.kubernetes.io/limit-rps",
	"nginx.ingress.kubernetes.io/limit-rpm",
	"nginx.ingress.kubernetes.io/limit-connections",
	"nginx.ingress.kubernetes.io/limit-burst-multiplier",
	"nginx.ingress.kubernetes.io/limit-whitelist",
	// traefik
	"traefik.ingress.kubernetes.io/rate-limit",
}

func (h *Handler) GetRateLimits(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Query("environment")

	if env == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment is required"})
		return
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	// Read rate limits from the CRD's per-environment ingress annotations
	envs, _, _ := unstructuredNestedSlice(app.Object, "spec", "environments")
	limits := map[string]string{}
	found := false
	for _, e := range envs {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := em["name"].(string); name == env {
			found = true
			if ingCfg, ok := em["ingress"].(map[string]interface{}); ok {
				if ann, ok := ingCfg["annotations"].(map[string]interface{}); ok {
					for _, key := range rateLimitAnnotationKeys {
						if v, ok := ann[key].(string); ok {
							limits[key] = v
						}
					}
				}
			}
			break
		}
	}

	if !found {
		c.JSON(http.StatusOK, gin.H{"limits": map[string]string{}, "ingressFound": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"limits": limits, "ingressFound": true, "ingress": appID})
}

func (h *Handler) UpdateRateLimits(c *gin.Context) {
	appID := c.Param("appId")

	var req struct {
		Environment string            `json:"environment" binding:"required"`
		Limits      map[string]string `json:"limits" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Validate annotation keys — only allow known rate limit annotations
	allowedKeys := make(map[string]bool)
	for _, k := range rateLimitAnnotationKeys {
		allowedKeys[k] = true
	}
	for key := range req.Limits {
		if !allowedKeys[key] {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid rate limit annotation: " + key})
			return
		}
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")

	// Build the annotations map: keep existing non-rate-limit annotations, then apply new limits
	envs, _, _ := unstructuredNestedSlice(app.Object, "spec", "environments")
	envIndex := -1
	for i, e := range envs {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := em["name"].(string); name == req.Environment {
			envIndex = i
			break
		}
	}

	if envIndex == -1 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "environment not found in app spec"})
		return
	}

	// Read current per-env ingress annotations
	existingAnnotations := map[string]interface{}{}
	if envMap, ok := envs[envIndex].(map[string]interface{}); ok {
		if ingCfg, ok := envMap["ingress"].(map[string]interface{}); ok {
			if ann, ok := ingCfg["annotations"].(map[string]interface{}); ok {
				existingAnnotations = ann
			}
		}
	}

	// Remove all rate limit keys from existing, then add new ones
	for _, key := range rateLimitAnnotationKeys {
		delete(existingAnnotations, key)
	}
	for key, value := range req.Limits {
		if value != "" {
			existingAnnotations[key] = value
		}
	}

	// Build a strategic merge patch for the VestaApp CRD
	envPatch := make([]interface{}, len(envs))
	for i := range envs {
		if i == envIndex {
			envPatch[i] = map[string]interface{}{
				"name": req.Environment,
				"ingress": map[string]interface{}{
					"annotations": existingAnnotations,
				},
			}
		} else {
			em, _ := envs[i].(map[string]interface{})
			envPatch[i] = map[string]interface{}{
				"name": em["name"],
			}
		}
	}

	patchData, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"environments": envPatch,
		},
	})

	if _, err := h.K8s.PatchResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID, patchData); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "rate limits updated", "limits": req.Limits})

	meta := make(map[string]interface{}, len(req.Limits))
	for k, v := range req.Limits {
		meta[k] = v
	}
	h.auditLog(c, "update_rate_limits", "app", appID, appID, project, req.Environment, meta)
}
