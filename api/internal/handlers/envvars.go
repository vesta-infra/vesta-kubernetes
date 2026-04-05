package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateAppEnvVars(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Param("env")

	var body struct {
		Data map[string]string `json:"data"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	if len(body.Data) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "data must contain at least one key"})
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
	cmName := fmt.Sprintf("%s-envvars", appID)

	existing, _ := h.K8s.GetResource(c.Request.Context(), k8s.ConfigMapGVR, namespace, cmName)
	if existing != nil {
		// Merge new keys into existing data
		existingData, _, _ := unstructuredNestedMap(existing.Object, "data")
		if existingData == nil {
			existingData = make(map[string]interface{})
		}
		for k, v := range body.Data {
			existingData[k] = v
		}
		existing.Object["data"] = existingData
		_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.ConfigMapGVR, namespace, existing)
	} else {
		data := make(map[string]interface{}, len(body.Data))
		for k, v := range body.Data {
			data[k] = v
		}
		obj := map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      cmName,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by":       "vesta",
					"kubernetes.getvesta.sh/project":     project,
					"kubernetes.getvesta.sh/app":         appID,
					"kubernetes.getvesta.sh/environment": env,
				},
			},
			"data": data,
		}
		_, err = h.K8s.CreateResource(c.Request.Context(), k8s.ConfigMapGVR, namespace, obj)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	keys := make([]string, 0, len(body.Data))
	for k := range body.Data {
		keys = append(keys, k)
	}

	c.JSON(http.StatusCreated, gin.H{
		"name":        cmName,
		"namespace":   namespace,
		"app":         appID,
		"environment": env,
		"keys":        keys,
	})

	h.auditLog(c, "create_envvar", "configmap", cmName, cmName, project, env,
		map[string]interface{}{"app": appID, "keyCount": len(keys)})
}

func (h *Handler) ListAppEnvVars(c *gin.Context) {
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
	cmName := fmt.Sprintf("%s-envvars", appID)

	cm, err := h.K8s.GetResource(c.Request.Context(), k8s.ConfigMapGVR, namespace, cmName)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"items": []interface{}{}, "total": 0})
		return
	}

	data, _, _ := unstructuredNestedMap(cm.Object, "data")
	values := make(map[string]string)
	keys := make([]string, 0)
	if data != nil {
		for k, v := range data {
			if s, ok := v.(string); ok {
				values[k] = s
				keys = append(keys, k)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"items": []map[string]interface{}{
			{
				"name":        cmName,
				"namespace":   namespace,
				"app":         appID,
				"environment": env,
				"keys":        keys,
				"values":      values,
			},
		},
		"total": 1,
	})
}

func (h *Handler) DeleteAppEnvVarKey(c *gin.Context) {
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
	cmName := fmt.Sprintf("%s-envvars", appID)

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.ConfigMapGVR, namespace, cmName)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no environment variables found"})
		return
	}

	data, _, _ := unstructuredNestedMap(existing.Object, "data")
	if data == nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "key not found"})
		return
	}

	if _, ok := data[key]; !ok {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "key not found"})
		return
	}

	delete(data, key)
	existing.Object["data"] = data

	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.ConfigMapGVR, namespace, existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.Status(http.StatusNoContent)

	h.auditLog(c, "delete_envvar", "configmap", cmName, cmName, project, env,
		map[string]interface{}{"app": appID, "key": key})
}
