package handlers

import (
	"encoding/json"
	"fmt"
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
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := fmt.Sprintf("%s-%s", project, env)

	// Try to find ingress for this app
	ingressName := appID
	ingress, err := h.K8s.GetResource(c.Request.Context(), k8s.IngressGVR, namespace, ingressName)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"limits": map[string]string{}, "ingressFound": false})
		return
	}

	annotations := ingress.GetAnnotations()
	limits := map[string]string{}
	for _, key := range rateLimitAnnotationKeys {
		if v, ok := annotations[key]; ok {
			limits[key] = v
		}
	}

	c.JSON(http.StatusOK, gin.H{"limits": limits, "ingressFound": true, "ingress": ingressName})
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
	namespace := fmt.Sprintf("%s-%s", project, req.Environment)

	ingressName := appID
	ingress, err := h.K8s.GetResource(c.Request.Context(), k8s.IngressGVR, namespace, ingressName)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "ingress not found"})
		return
	}

	annotations := ingress.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Remove all rate limit annotations first, then set new ones
	for _, key := range rateLimitAnnotationKeys {
		delete(annotations, key)
	}
	for key, value := range req.Limits {
		if value != "" {
			annotations[key] = value
		}
	}
	ingress.SetAnnotations(annotations)

	patchData, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": annotations,
		},
	})

	if _, err := h.K8s.PatchResource(c.Request.Context(), k8s.IngressGVR, namespace, ingressName, patchData); err != nil {
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
