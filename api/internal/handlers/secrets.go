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
	namespace := fmt.Sprintf("%s-%s", project, env)
	secretName := fmt.Sprintf("%s-secrets", appID)

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

	h.auditLog(c, "create_secret", "secret", secretName, secretName, project, env,
		map[string]interface{}{"app": appID, "keyCount": len(keys)})
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
	namespace := fmt.Sprintf("%s-%s", project, env)

	secretName := fmt.Sprintf("%s-secrets", appID)
	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretName)
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
	namespace := fmt.Sprintf("%s-%s", project, env)

	// One secret per app/env named "{appID}-secrets"
	secretName := fmt.Sprintf("%s-secrets", appID)
	secret, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretName)
	if err != nil {
		// No secret exists yet
		c.JSON(http.StatusOK, models.ListResponse{Items: []map[string]interface{}{}, Total: 0})
		return
	}

	spec, _, _ := unstructuredNestedMap(secret.Object, "spec")
	keys := extractSecretKeys(spec)

	items := []map[string]interface{}{
		{
			"name":        secretName,
			"namespace":   namespace,
			"type":        getNestedString(spec, "type"),
			"keys":        keys,
			"app":         appID,
			"environment": env,
		},
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: 1})
}

// RevealAppEnvSecretValues reveals values for an app/environment secret deterministically.
func (h *Handler) RevealAppEnvSecretValues(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Param("env")

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
	project := getNestedString(appSpec, "project")
	namespace := fmt.Sprintf("%s-%s", project, env)
	secretName := fmt.Sprintf("%s-secrets", appID)

	secret, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretName)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(secret.Object, "spec")
	keys := extractSecretKeys(spec)
	values := make(map[string]string)
	if data, ok := spec["data"].(map[string]interface{}); ok {
		for k, v := range data {
			if s, ok := v.(string); ok {
				values[k] = s
			}
		}
	}

	h.auditLog(c, "reveal_secret", "secret", secretName, secretName, project, env,
		map[string]interface{}{
			"app":       appID,
			"keys":      keys,
			"namespace": namespace,
		})

	c.JSON(http.StatusOK, gin.H{
		"id":          secretName,
		"name":        secretName,
		"namespace":   namespace,
		"type":        getNestedString(spec, "type"),
		"project":     project,
		"app":         appID,
		"environment": env,
		"values":      values,
	})
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
		Name     string `json:"name" binding:"required"`
		Registry string `json:"registry" binding:"required"`
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	spec := map[string]interface{}{
		"type": "kubernetes.io/dockerconfigjson",
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
			"namespace": vestaSystemNS,
			"labels": map[string]interface{}{
				"kubernetes.getvesta.sh/type": "registry",
			},
		},
		"spec": spec,
	}

	result, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaSecretGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        result.GetName(),
		"name":      result.GetName(),
		"registry":  req.Registry,
		"username":  req.Username,
		"type":      "kubernetes.io/dockerconfigjson",
		"createdAt": result.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	})
}

func (h *Handler) ListRegistrySecrets(c *gin.Context) {
	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, vestaSystemNS,
		"kubernetes.getvesta.sh/type=registry")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, item := range list.Items {
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		dc, _, _ := unstructuredNestedMap(spec, "dockerConfig")
		items = append(items, map[string]interface{}{
			"id":        item.GetName(),
			"name":      item.GetName(),
			"registry":  getNestedString(dc, "registry"),
			"username":  getNestedString(dc, "username"),
			"createdAt": item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) DeleteRegistrySecret(c *gin.Context) {
	name := c.Param("name")
	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaSecretGVR, vestaSystemNS, name); err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "registry secret not found"})
		return
	}
	c.Status(http.StatusNoContent)
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

// RevealSecretValues returns actual secret values. Admin-only with full audit trail.
func (h *Handler) RevealSecretValues(c *gin.Context) {
	secretID := c.Param("secretId")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	var namespace string
	var found bool
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

	secret, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, secretID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(secret.Object, "spec")
	labels := secret.GetLabels()
	keys := extractSecretKeys(spec)

	values := make(map[string]string)
	if data, ok := spec["data"].(map[string]interface{}); ok {
		for k, v := range data {
			if s, ok := v.(string); ok {
				values[k] = s
			}
		}
	}

	project := labels["kubernetes.getvesta.sh/project"]
	app := labels["kubernetes.getvesta.sh/app"]
	env := labels["kubernetes.getvesta.sh/environment"]

	h.auditLog(c, "reveal_secret", "secret", secretID, secretID, project, env,
		map[string]interface{}{
			"app":       app,
			"keys":      keys,
			"namespace": namespace,
		})

	c.JSON(http.StatusOK, gin.H{
		"id":          secretID,
		"name":        secretID,
		"namespace":   namespace,
		"type":        getNestedString(spec, "type"),
		"project":     project,
		"app":         app,
		"environment": env,
		"values":      values,
	})
}

// ─── Shared Secrets ──────────────────────────────────────────────────────────

// UpdateSharedSecret updates data on an existing shared secret across all its environments.
func (h *Handler) UpdateSharedSecret(c *gin.Context) {
	projectID := c.Param("projectId")
	name := c.Param("name")

	var req struct {
		Data       map[string]string `json:"data"`
		DeleteKeys []string          `json:"deleteKeys,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if len(req.Data) == 0 && len(req.DeleteKeys) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "data or deleteKeys required"})
		return
	}

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "",
		"kubernetes.getvesta.sh/project="+projectID+",kubernetes.getvesta.sh/shared=true")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	updated := 0
	for _, item := range list.Items {
		if item.GetName() != name {
			continue
		}
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		existingData, _, _ := unstructuredNestedMap(spec, "data")
		if existingData == nil {
			existingData = make(map[string]interface{})
		}
		for k, v := range req.Data {
			existingData[k] = v
		}
		for _, k := range req.DeleteKeys {
			delete(existingData, k)
		}
		spec["data"] = existingData
		item.Object["spec"] = spec
		if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaSecretGVR, item.GetNamespace(), &item); err == nil {
			updated++
		}
	}

	if updated == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "shared secret not found"})
		return
	}

	keys := make([]string, 0, len(req.Data))
	for k := range req.Data {
		keys = append(keys, k)
	}

	h.auditLog(c, "update_shared_secret", "secret", name, name, projectID, "",
		map[string]interface{}{"updatedKeys": keys, "deletedKeys": req.DeleteKeys})

	c.JSON(http.StatusOK, gin.H{"name": name, "message": "shared secret updated", "updatedEnvironments": updated})
}

// RevealSharedSecret reveals values of a shared secret. Admin-only, audit-logged.
func (h *Handler) RevealSharedSecret(c *gin.Context) {
	projectID := c.Param("projectId")
	name := c.Param("name")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "",
		"kubernetes.getvesta.sh/project="+projectID+",kubernetes.getvesta.sh/shared=true")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// Find the first matching secret (all envs share the same data)
	for _, item := range list.Items {
		if item.GetName() != name {
			continue
		}
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		values := make(map[string]string)
		if data, ok := spec["data"].(map[string]interface{}); ok {
			for k, v := range data {
				if s, ok := v.(string); ok {
					values[k] = s
				}
			}
		}

		h.auditLog(c, "reveal_secret", "secret", name, name, projectID, "",
			map[string]interface{}{
				"shared":    true,
				"keys":      extractSecretKeys(spec),
				"namespace": item.GetNamespace(),
			})

		c.JSON(http.StatusOK, gin.H{
			"id":     name,
			"name":   name,
			"values": values,
		})
		return
	}

	c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "shared secret not found"})
}

func (h *Handler) CreateSharedSecret(c *gin.Context) {
	projectID := c.Param("projectId")

	var req struct {
		Name         string            `json:"name" binding:"required"`
		Data         map[string]string `json:"data" binding:"required"`
		Environments []string          `json:"environments,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Resolve environments from the project if not specified
	environments := req.Environments
	if len(environments) == 0 {
		project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
		if err != nil {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
			return
		}
		spec, _, _ := unstructuredNestedMap(project.Object, "spec")
		if envs, ok := spec["environments"].([]interface{}); ok {
			for _, e := range envs {
				if em, ok := e.(map[string]interface{}); ok {
					if name, ok := em["name"].(string); ok {
						environments = append(environments, name)
					}
				}
			}
		}
		if len(environments) == 0 {
			environments = []string{"default"}
		}
	}

	data := make(map[string]interface{}, len(req.Data))
	for k, v := range req.Data {
		data[k] = v
	}

	var lastErr error
	for _, env := range environments {
		namespace := fmt.Sprintf("%s-%s", projectID, env)
		obj := map[string]interface{}{
			"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
			"kind":       "VestaSecret",
			"metadata": map[string]interface{}{
				"name":      req.Name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"kubernetes.getvesta.sh/project": projectID,
					"kubernetes.getvesta.sh/shared":  "true",
				},
			},
			"spec": map[string]interface{}{
				"type":    "Opaque",
				"project": projectID,
				"data":    data,
			},
		}

		existing, _ := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, req.Name)
		if existing != nil {
			existingSpec, _, _ := unstructuredNestedMap(existing.Object, "spec")
			existingSpec["data"] = data
			existing.Object["spec"] = existingSpec
			_, lastErr = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, existing)
		} else {
			_, lastErr = h.K8s.CreateResource(c.Request.Context(), k8s.VestaSecretGVR, namespace, obj)
		}
	}

	if lastErr != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: lastErr.Error()})
		return
	}

	keys := make([]string, 0, len(req.Data))
	for k := range req.Data {
		keys = append(keys, k)
	}

	c.JSON(http.StatusCreated, gin.H{
		"name":         req.Name,
		"project":      projectID,
		"keys":         keys,
		"environments": environments,
		"shared":       true,
	})

	h.auditLog(c, "create_shared_secret", "secret", req.Name, req.Name, projectID, "",
		map[string]interface{}{"keys": keys, "environments": environments})
}

func (h *Handler) ListSharedSecrets(c *gin.Context) {
	projectID := c.Param("projectId")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "",
		"kubernetes.getvesta.sh/project="+projectID+",kubernetes.getvesta.sh/shared=true")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// Group by name, collect environments
	type sharedEntry struct {
		Name         string   `json:"name"`
		Keys         []string `json:"keys"`
		Environments []string `json:"environments"`
		CreatedAt    string   `json:"createdAt"`
	}
	grouped := map[string]*sharedEntry{}
	for _, item := range list.Items {
		name := item.GetName()
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		labels := item.GetLabels()
		env := labels["kubernetes.getvesta.sh/environment"]
		if env == "" {
			// Derive env from namespace: {project}-{env}
			ns := item.GetNamespace()
			prefix := projectID + "-"
			if len(ns) > len(prefix) {
				env = ns[len(prefix):]
			}
		}

		if _, ok := grouped[name]; !ok {
			grouped[name] = &sharedEntry{
				Name:      name,
				Keys:      extractSecretKeys(spec),
				CreatedAt: item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
			}
		}
		if env != "" {
			grouped[name].Environments = append(grouped[name].Environments, env)
		}
	}

	items := make([]sharedEntry, 0, len(grouped))
	for _, v := range grouped {
		items = append(items, *v)
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) DeleteSharedSecret(c *gin.Context) {
	projectID := c.Param("projectId")
	name := c.Param("name")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "",
		"kubernetes.getvesta.sh/project="+projectID+",kubernetes.getvesta.sh/shared=true")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	deleted := 0
	for _, item := range list.Items {
		if item.GetName() == name {
			if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaSecretGVR, item.GetNamespace(), name); err == nil {
				deleted++
			}
		}
	}

	if deleted == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "shared secret not found"})
		return
	}

	h.auditLog(c, "delete_shared_secret", "secret", name, name, projectID, "", nil)
	c.Status(http.StatusNoContent)
}

// ─── Bind / Unbind Shared Secrets ────────────────────────────────────────────

func (h *Handler) BindSharedSecret(c *gin.Context) {
	appID := c.Param("appId")

	var req struct {
		Name        string `json:"name" binding:"required"`
		Environment string `json:"environment" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	runtime, _, _ := unstructuredNestedMap(spec, "runtime")
	if runtime == nil {
		runtime = map[string]interface{}{}
	}

	secrets, _ := runtime["secrets"].([]interface{})

	// Check if already bound for this environment
	for _, s := range secrets {
		if sm, ok := s.(map[string]interface{}); ok {
			if ref, ok := sm["secretRef"].(map[string]interface{}); ok {
				if ref["name"] == req.Name {
					envs, _ := sm["environments"].([]interface{})
					for _, e := range envs {
						if e == req.Environment {
							c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "shared secret already bound to this environment"})
							return
						}
					}
					// Same secret exists but for other environments — add this env
					envs = append(envs, req.Environment)
					sm["environments"] = envs
					runtime["secrets"] = secrets
					spec["runtime"] = runtime
					existing.Object["spec"] = spec
					if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, existing); err != nil {
						c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
						return
					}
					c.JSON(http.StatusOK, gin.H{"message": "shared secret bound", "name": req.Name, "environment": req.Environment, "app": appID})
					h.auditLog(c, "bind_shared_secret", "app", appID, appID, getNestedString(spec, "project"), "",
						map[string]interface{}{"secretName": req.Name, "environment": req.Environment})
					return
				}
			}
		}
	}

	secrets = append(secrets, map[string]interface{}{
		"secretRef":    map[string]interface{}{"name": req.Name},
		"environments": []interface{}{req.Environment},
	})
	runtime["secrets"] = secrets
	spec["runtime"] = runtime
	existing.Object["spec"] = spec

	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "shared secret bound", "name": req.Name, "environment": req.Environment, "app": appID})

	h.auditLog(c, "bind_shared_secret", "app", appID, appID, getNestedString(spec, "project"), "",
		map[string]interface{}{"secretName": req.Name, "environment": req.Environment})
}

func (h *Handler) UnbindSharedSecret(c *gin.Context) {
	appID := c.Param("appId")
	name := c.Param("name")
	env := c.Query("environment")

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	runtime, _, _ := unstructuredNestedMap(spec, "runtime")
	if runtime == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not bound"})
		return
	}

	secrets, _ := runtime["secrets"].([]interface{})
	found := false
	filtered := make([]interface{}, 0, len(secrets))
	for _, s := range secrets {
		if sm, ok := s.(map[string]interface{}); ok {
			if ref, ok := sm["secretRef"].(map[string]interface{}); ok {
				if ref["name"] == name {
					found = true
					if env != "" {
						// Remove only the specified environment
						envs, _ := sm["environments"].([]interface{})
						newEnvs := make([]interface{}, 0, len(envs))
						for _, e := range envs {
							if e != env {
								newEnvs = append(newEnvs, e)
							}
						}
						if len(newEnvs) > 0 {
							sm["environments"] = newEnvs
							filtered = append(filtered, sm)
						}
						// If newEnvs is empty, we drop the entire binding
					}
					// If env=="", drop the entire binding (remove from all environments)
					continue
				}
			}
		}
		filtered = append(filtered, s)
	}

	if !found {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "secret not bound"})
		return
	}

	runtime["secrets"] = filtered
	spec["runtime"] = runtime
	existing.Object["spec"] = spec

	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "unbind_shared_secret", "app", appID, appID, getNestedString(spec, "project"), "",
		map[string]interface{}{"secretName": name, "environment": env})

	c.Status(http.StatusNoContent)
}

func (h *Handler) ListAppSharedSecrets(c *gin.Context) {
	appID := c.Param("appId")

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	runtime, _, _ := unstructuredNestedMap(spec, "runtime")
	if runtime == nil {
		c.JSON(http.StatusOK, models.ListResponse{Items: []interface{}{}, Total: 0})
		return
	}

	secrets, _ := runtime["secrets"].([]interface{})
	bound := make([]map[string]interface{}, 0)
	for _, s := range secrets {
		if sm, ok := s.(map[string]interface{}); ok {
			if ref, ok := sm["secretRef"].(map[string]interface{}); ok {
				if name, ok := ref["name"].(string); ok {
					envs, _ := sm["environments"].([]interface{})
					envStrs := make([]string, 0, len(envs))
					for _, e := range envs {
						if es, ok := e.(string); ok {
							envStrs = append(envStrs, es)
						}
					}
					bound = append(bound, map[string]interface{}{
						"name":         name,
						"environments": envStrs,
					})
				}
			}
		}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: bound, Total: len(bound)})
}
