package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateAppEnvSecret(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Param("env")

	var req models.CreateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if req.Type == "" {
		req.Type = "Opaque"
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := fmt.Sprintf("%s-%s-%s", project, appID, env)
	secretName := "app-secrets"

	// Try to get existing secret and merge keys
	existing, _ := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretName)
	if existing != nil {
		// Merge new keys into existing data
		existingSpec, _, _ := unstructuredNestedMap(existing.Object, "spec")
		existingData, _, _ := unstructuredNestedMap(existingSpec, "data")
		if existingData == nil {
			existingData = make(map[string]interface{})
		}
		for k, v := range req.Data {
			existingData[k] = v
		}
		existingSpec["data"] = existingData
		existing.Object["spec"] = existingSpec
		_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, existing)
	} else {
		// Create new secret
		spec := map[string]interface{}{
			"type":        req.Type,
			"project":     project,
			"app":         appID,
			"environment": env,
		}
		if req.Data != nil {
			data := make(map[string]interface{}, len(req.Data))
			for k, v := range req.Data {
				data[k] = v
			}
			spec["data"] = data
		}

		obj := map[string]interface{}{
			"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
			"kind":       "VestaSecret",
			"metadata": map[string]interface{}{
				"name":      secretName,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"kubernetes.getvesta.sh/project":     project,
					"kubernetes.getvesta.sh/app":         appID,
					"kubernetes.getvesta.sh/environment": env,
				},
			},
			"spec": spec,
		}
		_, err = h.K8s.CreateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, obj)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	keys := make([]string, 0)
	if req.Data != nil {
		for k := range req.Data {
			keys = append(keys, k)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"name":        secretName,
		"namespace":   namespace,
		"app":         appID,
		"environment": env,
		"type":        req.Type,
		"keys":        keys,
	})
}

func (h *Handler) DeleteAppEnvSecretKey(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Param("env")
	key := c.Param("key")

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := fmt.Sprintf("%s-%s-%s", project, appID, env)

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, "app-secrets")
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no secrets found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	data, _, _ := unstructuredNestedMap(spec, "data")
	if data == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "key not found"})
		return
	}

	delete(data, key)
	spec["data"] = data
	existing.Object["spec"] = spec

	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) ListAppEnvSecrets(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Param("env")

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := fmt.Sprintf("%s-%s-%s", project, appID, env)

	// Only one secret per app/env named "app-secrets"
	secret, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, "app-secrets")
	if err != nil {
		// No secret exists yet
		c.JSON(http.StatusOK, models.ListResponse{Items: []map[string]interface{}{}, Total: 0})
		return
	}

	spec, _, _ := unstructuredNestedMap(secret.Object, "spec")
	keys := extractSecretKeys(spec)

	items := []map[string]interface{}{
		{
			"name":        "app-secrets",
			"namespace":   namespace,
			"type":        getNestedString(spec, "type"),
			"keys":        keys,
			"app":         appID,
			"environment": env,
		},
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: 1})
}

func (h *Handler) ListSecrets(c *gin.Context) {
	project := c.Query("project")
	labelSelector := ""
	if project != "" {
		labelSelector = "kubernetes.getvesta.sh/project=" + project
	}

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "", labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		labels := item.GetLabels()
		keys := extractSecretKeys(spec)
		items[i] = map[string]interface{}{
			"id":          item.GetName(),
			"name":        item.GetName(),
			"namespace":   item.GetNamespace(),
			"type":        getNestedString(spec, "type"),
			"keys":        keys,
			"project":     labels["kubernetes.getvesta.sh/project"],
			"app":         labels["kubernetes.getvesta.sh/app"],
			"environment": labels["kubernetes.getvesta.sh/environment"],
			"createdAt":   item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
		}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) UpdateSecret(c *gin.Context) {
	secretID := c.Param("secretId")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	var found bool
	var namespace string
	for _, item := range list.Items {
		if item.GetName() == secretID {
			namespace = item.GetNamespace()
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not found"})
		return
	}

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not found"})
		return
	}

	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	if data, ok := patch["data"].(map[string]interface{}); ok {
		spec["data"] = data
	}
	existing.Object["spec"] = spec

	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": secretID, "message": "secret updated"})
}

func (h *Handler) DeleteSecret(c *gin.Context) {
	secretID := c.Param("secretId")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	for _, item := range list.Items {
		if item.GetName() == secretID {
			if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaSecretGVR, item.GetNamespace(), secretID); err != nil {
				c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
				return
			}
			c.Status(http.StatusNoContent)
			return
		}
	}
	c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not found"})
}

func (h *Handler) CreateRegistrySecret(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Project     string `json:"project" binding:"required"`
		Environment string `json:"environment" binding:"required"`
		Registry    string `json:"registry" binding:"required"`
		Username    string `json:"username" binding:"required"`
		Password    string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	namespace := fmt.Sprintf("%s-%s", req.Project, req.Environment)

	spec := map[string]interface{}{
		"type":        "kubernetes.io/dockerconfigjson",
		"project":     req.Project,
		"environment": req.Environment,
		"dockerConfig": map[string]interface{}{
			"registry": req.Registry,
			"username": req.Username,
			"password": req.Password,
		},
	}

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaSecret",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"kubernetes.getvesta.sh/project":     req.Project,
				"kubernetes.getvesta.sh/environment": req.Environment,
			},
		},
		"spec": spec,
	}

	result, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        result.GetName(),
		"name":      result.GetName(),
		"namespace": namespace,
		"type":      "kubernetes.io/dockerconfigjson",
		"createdAt": result.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	})
}

func extractSecretKeys(spec map[string]interface{}) []string {
	keys := make([]string, 0)
	if data, ok := spec["data"].(map[string]interface{}); ok {
		for k := range data {
			keys = append(keys, k)
		}
	}
	return keys
}
