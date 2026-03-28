package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// Default pod size presets used when no VestaConfig is found
var defaultPodSizes = []map[string]interface{}{
	{"name": "small", "cpu": "250m", "memory": "256Mi", "cpuLimit": "500m", "memoryLimit": "512Mi"},
	{"name": "medium", "cpu": "500m", "memory": "512Mi", "cpuLimit": "1", "memoryLimit": "1Gi"},
	{"name": "large", "cpu": "1", "memory": "1Gi", "cpuLimit": "2", "memoryLimit": "2Gi"},
	{"name": "xlarge", "cpu": "2", "memory": "2Gi", "cpuLimit": "4", "memoryLimit": "4Gi"},
}

func (h *Handler) ListPodSizes(c *gin.Context) {
	configs, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaConfigGVR, vestaSystemNS, "")
	if err != nil || len(configs.Items) == 0 {
		c.JSON(http.StatusOK, models.ListResponse{Items: defaultPodSizes, Total: len(defaultPodSizes)})
		return
	}

	spec, _, _ := unstructuredNestedMap(configs.Items[0].Object, "spec")
	podSizeList, ok := spec["podSizeList"].([]interface{})
	if !ok || len(podSizeList) == 0 {
		c.JSON(http.StatusOK, models.ListResponse{Items: defaultPodSizes, Total: len(defaultPodSizes)})
		return
	}

	sizes := make([]map[string]interface{}, 0, len(podSizeList))
	for _, item := range podSizeList {
		preset, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		size := map[string]interface{}{"name": preset["name"]}
		if reqs, ok := preset["requests"].(map[string]interface{}); ok {
			if v, ok := reqs["cpu"]; ok {
				size["cpu"] = v
			}
			if v, ok := reqs["memory"]; ok {
				size["memory"] = v
			}
		}
		if lims, ok := preset["limits"].(map[string]interface{}); ok {
			if v, ok := lims["cpu"]; ok {
				size["cpuLimit"] = v
			}
			if v, ok := lims["memory"]; ok {
				size["memoryLimit"] = v
			}
		}
		sizes = append(sizes, size)
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: sizes, Total: len(sizes)})
}

func (h *Handler) CreateApp(c *gin.Context) {
	projectID := c.Param("projectId")
	var req models.CreateAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Validate the project has at least one environment
	project, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "project not found"})
		return
	}
	projectSpec, _, _ := unstructuredNestedMap(project.Object, "spec")
	projectEnvs, _ := projectSpec["environments"].([]interface{})
	if len(projectEnvs) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: "project has no environments; create at least one environment before adding apps",
		})
		return
	}

	spec := map[string]interface{}{
		"project": projectID,
	}
	if len(req.Environments) > 0 {
		envs := make([]interface{}, len(req.Environments))
		for i, e := range req.Environments {
			envConfig := map[string]interface{}{
				"name": e.Name,
			}
			if e.Replicas != nil {
				envConfig["replicas"] = *e.Replicas
			}
			if e.Autoscale != nil {
				// Convert targetCPU to metrics array format expected by operator
				autoscale := make(map[string]interface{})
				for k, v := range e.Autoscale {
					if k == "targetCPU" {
						// Convert targetCPU -> metrics array
						if cpu, ok := v.(float64); ok {
							cpuInt := int32(cpu)
							autoscale["metrics"] = []map[string]interface{}{
								{"type": "cpu", "targetAverageUtilization": cpuInt},
							}
						}
					} else {
						autoscale[k] = v
					}
				}
				envConfig["autoscale"] = autoscale
			}
			if e.Resources != nil {
				envConfig["resources"] = e.Resources
			}
			envs[i] = envConfig
		}
		spec["environments"] = envs
	}
	if req.Git != nil {
		spec["git"] = req.Git
	}
	if req.Build != nil {
		spec["build"] = req.Build
	}
	if req.Image != nil {
		spec["image"] = req.Image
	}
	if req.Runtime != nil {
		spec["runtime"] = req.Runtime
	} else {
		spec["runtime"] = map[string]interface{}{"port": 3000}
	}
	if req.Resources != nil {
		spec["resources"] = req.Resources
	}
	if req.HealthCheck != nil {
		spec["healthCheck"] = req.HealthCheck
	}
	if req.Ingress != nil {
		spec["ingress"] = req.Ingress
	}
	if req.Addons != nil {
		addons := make([]interface{}, len(req.Addons))
		for i, a := range req.Addons {
			addons[i] = a
		}
		spec["addons"] = addons
	}
	if req.CustomConfig != nil {
		spec["customConfig"] = req.CustomConfig
	}

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaApp",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": vestaSystemNS,
			"labels": map[string]interface{}{
				"kubernetes.getvesta.sh/project": projectID,
			},
		},
		"spec": spec,
	}

	result, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        result.GetName(),
		"name":      result.GetName(),
		"project":   projectID,
		"createdAt": result.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	})

	h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
		Type:        services.EventAppCreated,
		ProjectID:   projectID,
		AppID:       result.GetName(),
		TriggeredBy: c.GetString("userId"),
		Message:     fmt.Sprintf("App %s created in project %s", result.GetName(), projectID),
	})
}

func (h *Handler) ListApps(c *gin.Context) {
	projectID := c.Query("project")
	labelSelector := ""
	if projectID != "" {
		labelSelector = "kubernetes.getvesta.sh/project=" + projectID
	}

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		status, _, _ := unstructuredNestedMap(item.Object, "status")
		environments, _, _ := unstructuredNestedSlice(item.Object, "spec", "environments")
		items[i] = map[string]interface{}{
			"id":           item.GetName(),
			"name":         item.GetName(),
			"namespace":    item.GetNamespace(),
			"project":      getNestedString(spec, "project"),
			"environments": environments,
			"status":       status,
			"createdAt":    item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
		}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) ListProjectApps(c *gin.Context) {
	projectID := c.Param("projectId")
	labelSelector := "kubernetes.getvesta.sh/project=" + projectID

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		status, _, _ := unstructuredNestedMap(item.Object, "status")
		environments, _, _ := unstructuredNestedSlice(item.Object, "spec", "environments")
		items[i] = map[string]interface{}{
			"id":           item.GetName(),
			"name":         item.GetName(),
			"project":      projectID,
			"environments": environments,
			"spec":         spec,
			"status":       status,
			"createdAt":    item.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
		}
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) GetApp(c *gin.Context) {
	appID := c.Param("appId")

	result, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(result.Object, "spec")
	status, _, _ := unstructuredNestedMap(result.Object, "status")
	environments, _, _ := unstructuredNestedSlice(result.Object, "spec", "environments")
	project := getNestedString(spec, "project")

	c.JSON(http.StatusOK, gin.H{
		"id":           result.GetName(),
		"name":         result.GetName(),
		"namespace":    result.GetNamespace(),
		"project":      project,
		"environments": environments,
		"spec":         spec,
		"status":       status,
		"createdAt":    result.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	})
}

func (h *Handler) UpdateApp(c *gin.Context) {
	appID := c.Param("appId")

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
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

	result, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, existing)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": result.GetName(), "name": result.GetName()})
}

func (h *Handler) DeleteApp(c *gin.Context) {
	appID := c.Param("appId")

	// Resolve project for notification before deleting
	var projectID string
	if existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID); err == nil {
		spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
		projectID = getNestedString(spec, "project")
	}

	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if projectID != "" {
		h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
			Type:        services.EventAppDeleted,
			ProjectID:   projectID,
			AppID:       appID,
			TriggeredBy: c.GetString("userId"),
			Message:     fmt.Sprintf("App %s deleted from project %s", appID, projectID),
		})
	}

	c.Status(http.StatusNoContent)
}

func getNestedString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func unstructuredNestedMap(obj map[string]interface{}, fields ...string) (map[string]interface{}, bool, error) {
	var current interface{} = obj
	for _, f := range fields {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		current, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	result, ok := current.(map[string]interface{})
	return result, ok, nil
}

func unstructuredNestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	var current interface{} = obj
	for _, f := range fields {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		current, ok = m[f]
		if !ok {
			return nil, false, nil
		}
	}
	result, ok := current.([]interface{})
	return result, ok, nil
}
