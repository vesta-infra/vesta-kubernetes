package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) StreamLogs(c *gin.Context) {
	appId := c.Param("appId")
	env := c.Query("environment")

	if env == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment query param is required"})
		return
	}

	// Resolve the app to get project
	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	project := getNestedString(spec, "project")
	targetNS := fmt.Sprintf("%s-%s", project, env)

	tailLines := int64(200)
	if tl := c.Query("tail"); tl != "" {
		if n, err := strconv.ParseInt(tl, 10, 64); err == nil && n > 0 {
			tailLines = n
			if tailLines > 5000 {
				tailLines = 5000
			}
		}
	}

	previous := c.Query("previous") == "true"
	container := c.Query("container")
	podName := c.Query("pod")

	// If a specific pod was requested, get its logs directly
	if podName != "" {
		logs, err := h.K8s.GetPodLogs(c.Request.Context(), targetNS, podName, container, tailLines, previous)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to get logs: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"pod":  podName,
			"logs": logs,
		})
		return
	}

	// Otherwise list all pods for the app and return logs for each
	labelSelector := fmt.Sprintf("app=%s", appId)
	pods, err := h.K8s.ListPods(c.Request.Context(), targetNS, labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to list pods: %v", err)})
		return
	}

	type podLogEntry struct {
		Pod       string `json:"pod"`
		Status    string `json:"status"`
		Restarts  int32  `json:"restarts"`
		Logs      string `json:"logs"`
		Container string `json:"container"`
	}

	results := make([]podLogEntry, 0, len(pods))
	for _, pod := range pods {
		podPhase := string(pod.Status.Phase)
		restarts := int32(0)
		if len(pod.Status.ContainerStatuses) > 0 {
			restarts = pod.Status.ContainerStatuses[0].RestartCount
		}

		targetContainer := container
		if targetContainer == "" && len(pod.Spec.Containers) > 0 {
			targetContainer = pod.Spec.Containers[0].Name
		}

		logs, err := h.K8s.GetPodLogs(c.Request.Context(), targetNS, pod.Name, targetContainer, tailLines, previous)
		if err != nil {
			logs = fmt.Sprintf("[error fetching logs: %v]", err)
		}

		results = append(results, podLogEntry{
			Pod:       pod.Name,
			Status:    podPhase,
			Restarts:  restarts,
			Logs:      logs,
			Container: targetContainer,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"environment": env,
		"namespace":   targetNS,
		"pods":        results,
		"total":       len(results),
	})
}

func (h *Handler) GetMetrics(c *gin.Context) {
	appId := c.Param("appId")
	env := c.Query("environment")

	if env == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment query param is required"})
		return
	}

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}
	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	project := getNestedString(spec, "project")
	targetNS := fmt.Sprintf("%s-%s", project, env)

	// Get pods to report pod-level status/resource info
	labelSelector := fmt.Sprintf("app=%s", appId)
	pods, err := h.K8s.ListPods(c.Request.Context(), targetNS, labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to list pods: %v", err)})
		return
	}

	type podMetric struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Ready     bool   `json:"ready"`
		Restarts  int32  `json:"restarts"`
		CPUReq    string `json:"cpuRequest"`
		MemReq    string `json:"memoryRequest"`
		CPULim    string `json:"cpuLimit"`
		MemLim    string `json:"memoryLimit"`
		StartedAt string `json:"startedAt,omitempty"`
		NodeName  string `json:"nodeName"`
	}

	podMetrics := make([]podMetric, 0, len(pods))
	totalReady := 0
	for _, pod := range pods {
		ready := false
		restarts := int32(0)
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				ready = true
			}
			restarts += cs.RestartCount
		}
		if ready {
			totalReady++
		}

		cpuReq, memReq, cpuLim, memLim := "", "", "", ""
		if len(pod.Spec.Containers) > 0 {
			c0 := pod.Spec.Containers[0]
			if q, ok := c0.Resources.Requests["cpu"]; ok {
				cpuReq = q.String()
			}
			if q, ok := c0.Resources.Requests["memory"]; ok {
				memReq = q.String()
			}
			if q, ok := c0.Resources.Limits["cpu"]; ok {
				cpuLim = q.String()
			}
			if q, ok := c0.Resources.Limits["memory"]; ok {
				memLim = q.String()
			}
		}

		startedAt := ""
		if pod.Status.StartTime != nil {
			startedAt = pod.Status.StartTime.Format("2006-01-02T15:04:05Z")
		}

		podMetrics = append(podMetrics, podMetric{
			Name:      pod.Name,
			Status:    string(pod.Status.Phase),
			Ready:     ready,
			Restarts:  restarts,
			CPUReq:    cpuReq,
			MemReq:    memReq,
			CPULim:    cpuLim,
			MemLim:    memLim,
			StartedAt: startedAt,
			NodeName:  pod.Spec.NodeName,
		})
	}

	// Get the Deployment to report desired/available replicas
	deploy, err := h.K8s.GetResource(c.Request.Context(), k8s.DeploymentGVR, targetNS, appId)
	deployInfo := map[string]interface{}{}
	if err == nil {
		dSpec, _, _ := unstructuredNestedMap(deploy.Object, "spec")
		dStatus, _, _ := unstructuredNestedMap(deploy.Object, "status")
		deployInfo["desiredReplicas"] = dSpec["replicas"]
		deployInfo["readyReplicas"] = dStatus["readyReplicas"]
		deployInfo["availableReplicas"] = dStatus["availableReplicas"]
		deployInfo["updatedReplicas"] = dStatus["updatedReplicas"]
	}

	c.JSON(http.StatusOK, gin.H{
		"environment": env,
		"namespace":   targetNS,
		"deployment":  deployInfo,
		"pods":        podMetrics,
		"totalPods":   len(podMetrics),
		"readyPods":   totalReady,
	})
}
