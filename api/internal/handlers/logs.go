package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	labelSelector := fmt.Sprintf("kubernetes.getvesta.sh/app=%s", appId)
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
	labelSelector := fmt.Sprintf("kubernetes.getvesta.sh/app=%s", appId)
	pods, err := h.K8s.ListPods(c.Request.Context(), targetNS, labelSelector)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to list pods: %v", err)})
		return
	}

	type containerInfo struct {
		Name     string `json:"name"`
		Image    string `json:"image"`
		Ready    bool   `json:"ready"`
		State    string `json:"state"`
		Restarts int32  `json:"restarts"`
		CPUReq   string `json:"cpuRequest,omitempty"`
		MemReq   string `json:"memoryRequest,omitempty"`
		CPULim   string `json:"cpuLimit,omitempty"`
		MemLim   string `json:"memoryLimit,omitempty"`
		CPUUsage string `json:"cpuUsage,omitempty"`
		MemUsage string `json:"memoryUsage,omitempty"`
	}

	type podMetric struct {
		Name       string          `json:"name"`
		Status     string          `json:"status"`
		Ready      bool            `json:"ready"`
		Restarts   int32           `json:"restarts"`
		CPUReq     string          `json:"cpuRequest"`
		MemReq     string          `json:"memoryRequest"`
		CPULim     string          `json:"cpuLimit"`
		MemLim     string          `json:"memoryLimit"`
		CPUUsage   string          `json:"cpuUsage,omitempty"`
		MemUsage   string          `json:"memoryUsage,omitempty"`
		StartedAt  string          `json:"startedAt,omitempty"`
		Age        string          `json:"age,omitempty"`
		NodeName   string          `json:"nodeName"`
		IP         string          `json:"ip,omitempty"`
		Containers []containerInfo `json:"containers,omitempty"`
	}

	// Get live metrics from metrics-server (best effort)
	liveMetrics, _ := h.K8s.GetPodMetrics(c.Request.Context(), targetNS, labelSelector)

	now := time.Now()
	podMetrics := make([]podMetric, 0, len(pods))
	totalReady := 0
	var totalCPUUsageNano, totalMemUsageBytes int64
	var totalCPUReqNano, totalMemReqBytes int64
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

		// Aggregate CPU/mem requests/limits across all containers
		var podCPUReqNano, podMemReqBytes, podCPULimNano, podMemLimBytes int64
		containers := make([]containerInfo, 0, len(pod.Spec.Containers))
		for _, ctr := range pod.Spec.Containers {
			ci := containerInfo{
				Name:  ctr.Name,
				Image: ctr.Image,
			}
			if q, ok := ctr.Resources.Requests["cpu"]; ok {
				ci.CPUReq = q.String()
				podCPUReqNano += q.ScaledValue(0) * 1000000000
				if q.MilliValue() > 0 {
					podCPUReqNano = 0
					// Use MilliValue for precision
				}
			}
			if q, ok := ctr.Resources.Requests["memory"]; ok {
				ci.MemReq = q.String()
				podMemReqBytes += q.Value()
			}
			if q, ok := ctr.Resources.Limits["cpu"]; ok {
				ci.CPULim = q.String()
			}
			if q, ok := ctr.Resources.Limits["memory"]; ok {
				ci.MemLim = q.String()
			}
			// Container status
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Name == ctr.Name {
					ci.Ready = cs.Ready
					ci.Restarts = cs.RestartCount
					if cs.State.Running != nil {
						ci.State = "running"
					} else if cs.State.Waiting != nil {
						ci.State = cs.State.Waiting.Reason
					} else if cs.State.Terminated != nil {
						ci.State = cs.State.Terminated.Reason
					}
					break
				}
			}
			containers = append(containers, ci)
		}

		// Per-container live metrics
		if pm, ok := liveMetrics[pod.Name]; ok {
			for i, cm := range pm.Containers {
				for j := range containers {
					if containers[j].Name == cm.Name {
						containers[j].CPUUsage = cm.CPU
						containers[j].MemUsage = cm.Memory
					}
				}
				_ = i
			}
		}

		// Calculate aggregated requests via resource.Quantity for accuracy
		cpuReq, memReq, cpuLim, memLim := "", "", "", ""
		for _, ctr := range pod.Spec.Containers {
			if q, ok := ctr.Resources.Requests["cpu"]; ok {
				cpuReq = q.String()
				totalCPUReqNano += q.MilliValue() * 1000000
			}
			if q, ok := ctr.Resources.Requests["memory"]; ok {
				memReq = q.String()
				totalMemReqBytes += q.Value()
			}
			if q, ok := ctr.Resources.Limits["cpu"]; ok {
				cpuLim = q.String()
				podCPULimNano += q.MilliValue() * 1000000
			}
			if q, ok := ctr.Resources.Limits["memory"]; ok {
				memLim = q.String()
				podMemLimBytes += q.Value()
			}
		}

		startedAt := ""
		age := ""
		if pod.Status.StartTime != nil {
			startedAt = pod.Status.StartTime.Format("2006-01-02T15:04:05Z")
			dur := now.Sub(pod.Status.StartTime.Time)
			age = formatDuration(dur)
		}

		pm := podMetric{
			Name:       pod.Name,
			Status:     string(pod.Status.Phase),
			Ready:      ready,
			Restarts:   restarts,
			CPUReq:     cpuReq,
			MemReq:     memReq,
			CPULim:     cpuLim,
			MemLim:     memLim,
			CPUUsage:   liveMetrics[pod.Name].CPU,
			MemUsage:   liveMetrics[pod.Name].Memory,
			StartedAt:  startedAt,
			Age:        age,
			NodeName:   pod.Spec.NodeName,
			IP:         pod.Status.PodIP,
			Containers: containers,
		}

		// Accumulate total usage
		if mu, ok := liveMetrics[pod.Name]; ok {
			totalCPUUsageNano += k8s.ParseCPUNano(mu.CPU)
			totalMemUsageBytes += k8s.ParseMemBytes(mu.Memory)
		}

		podMetrics = append(podMetrics, pm)
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

	// Get HPA info if it exists
	hpaInfo := map[string]interface{}{}
	hpaGVR := schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}
	hpa, hpaErr := h.K8s.GetResource(c.Request.Context(), hpaGVR, targetNS, appId)
	if hpaErr == nil {
		hpaSpec, _, _ := unstructuredNestedMap(hpa.Object, "spec")
		hpaStatus, _, _ := unstructuredNestedMap(hpa.Object, "status")
		hpaInfo["minReplicas"] = hpaSpec["minReplicas"]
		hpaInfo["maxReplicas"] = hpaSpec["maxReplicas"]
		hpaInfo["currentReplicas"] = hpaStatus["currentReplicas"]
		hpaInfo["desiredReplicas"] = hpaStatus["desiredReplicas"]
		if conditions, ok := hpaStatus["conditions"].([]interface{}); ok {
			hpaInfo["conditions"] = conditions
		}
		if currentMetrics, ok := hpaStatus["currentMetrics"].([]interface{}); ok {
			hpaInfo["currentMetrics"] = currentMetrics
		}
	}

	// Build summary with totals
	summary := map[string]interface{}{
		"totalCPUUsage":    k8s.FormatCPUNano(totalCPUUsageNano),
		"totalMemoryUsage": k8s.FormatMemBytes(totalMemUsageBytes),
		"totalCPURequest":  k8s.FormatCPUNano(totalCPUReqNano),
		"totalMemoryRequest": k8s.FormatMemBytes(totalMemReqBytes),
	}
	if totalCPUReqNano > 0 {
		summary["cpuUtilization"] = float64(totalCPUUsageNano) / float64(totalCPUReqNano) * 100
	}
	if totalMemReqBytes > 0 {
		summary["memoryUtilization"] = float64(totalMemUsageBytes) / float64(totalMemReqBytes) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"environment": env,
		"namespace":   targetNS,
		"deployment":  deployInfo,
		"autoscaling": hpaInfo,
		"summary":     summary,
		"pods":        podMetrics,
		"totalPods":   len(podMetrics),
		"readyPods":   totalReady,
	})
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	if h > 0 {
		return fmt.Sprintf("%dd%dh", days, h)
	}
	return fmt.Sprintf("%dd", days)
}

// GetPrometheusMetrics returns time-series metrics from Prometheus for an app.
func (h *Handler) GetPrometheusMetrics(c *gin.Context) {
	appId := c.Param("appId")
	env := c.Query("environment")
	metric := c.Query("metric")
	timeRange := c.Query("range")

	if env == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment query param is required"})
		return
	}
	if metric == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "metric query param is required"})
		return
	}

	// Resolve Prometheus URL: check VestaConfig first, then auto-discover
	prometheusURL := ""
	if cfg, err := h.K8s.GetClusterResource(c.Request.Context(), k8s.VestaConfigGVR, "vesta"); err == nil {
		spec, _, _ := unstructuredNestedMap(cfg.Object, "spec")
		if u, ok := spec["prometheusUrl"].(string); ok && u != "" {
			prometheusURL = u
		}
	}
	if prometheusURL == "" {
		prometheusURL = h.K8s.DiscoverPrometheusURL(c.Request.Context())
	}
	if prometheusURL == "" {
		c.JSON(http.StatusOK, gin.H{
			"available": false,
			"message":   "Prometheus not configured. Set spec.prometheusUrl in VestaConfig or install Prometheus in the monitoring namespace.",
		})
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

	// Parse time range
	rangeDuration := 1 * time.Hour
	step := 30 * time.Second
	switch timeRange {
	case "6h":
		rangeDuration = 6 * time.Hour
		step = 2 * time.Minute
	case "24h":
		rangeDuration = 24 * time.Hour
		step = 5 * time.Minute
	case "7d":
		rangeDuration = 7 * 24 * time.Hour
		step = 30 * time.Minute
	case "1h":
		// defaults
	}

	end := time.Now()
	start := end.Add(-rangeDuration)
	podSelector := fmt.Sprintf("namespace=\"%s\",pod=~\"%s-.*\"", targetNS, appId)
	containerFilter := fmt.Sprintf("container!=\"\",%s", podSelector)

	// Build the PromQL query based on the requested metric
	var query string
	switch metric {
	case "cpu":
		query = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{%s}[5m])) by (pod)`, containerFilter)
	case "memory":
		query = fmt.Sprintf(`sum(container_memory_working_set_bytes{%s}) by (pod)`, containerFilter)
	case "network_rx":
		query = fmt.Sprintf(`sum(rate(container_network_receive_bytes_total{%s}[5m])) by (pod)`, podSelector)
	case "network_tx":
		query = fmt.Sprintf(`sum(rate(container_network_transmit_bytes_total{%s}[5m])) by (pod)`, podSelector)
	case "restarts":
		query = fmt.Sprintf(`sum(increase(kube_pod_container_status_restarts_total{namespace="%s",pod=~"%s-.*"}[1h])) by (pod)`, targetNS, appId)
	case "http_rate":
		trafikSvc := fmt.Sprintf(`%s-%s-.*@kubernetes`, targetNS, appId)
		traefik := fmt.Sprintf(`sum(rate(traefik_service_requests_total{exported_service=~"%s"}[5m])) or sum(rate(traefik_service_requests_total{service=~"%s"}[5m]))`, trafikSvc, trafikSvc)
		nginx := fmt.Sprintf(`sum(rate(nginx_ingress_controller_requests{exported_namespace="%s",ingress="%s"}[5m])) or sum(rate(nginx_ingress_controller_requests{namespace="%s",ingress="%s"}[5m]))`, targetNS, appId, targetNS, appId)
		query = fmt.Sprintf(`%s or %s`, traefik, nginx)
	case "http_errors":
		trafikSvc := fmt.Sprintf(`%s-%s-.*@kubernetes`, targetNS, appId)
		traefik := fmt.Sprintf(`(sum(rate(traefik_service_requests_total{exported_service=~"%s",code=~"[45].."}[5m])) or sum(rate(traefik_service_requests_total{service=~"%s",code=~"[45].."}[5m]))) / (sum(rate(traefik_service_requests_total{exported_service=~"%s"}[5m])) or sum(rate(traefik_service_requests_total{service=~"%s"}[5m]))) * 100`, trafikSvc, trafikSvc, trafikSvc, trafikSvc)
		nginx := fmt.Sprintf(`(sum(rate(nginx_ingress_controller_requests{exported_namespace="%s",ingress="%s",status=~"[45].."}[5m])) or sum(rate(nginx_ingress_controller_requests{namespace="%s",ingress="%s",status=~"[45].."}[5m]))) / (sum(rate(nginx_ingress_controller_requests{exported_namespace="%s",ingress="%s"}[5m])) or sum(rate(nginx_ingress_controller_requests{namespace="%s",ingress="%s"}[5m]))) * 100`, targetNS, appId, targetNS, appId, targetNS, appId, targetNS, appId)
		query = fmt.Sprintf(`%s or %s`, traefik, nginx)
	case "http_latency_p95":
		trafikSvc := fmt.Sprintf(`%s-%s-.*@kubernetes`, targetNS, appId)
		traefik := fmt.Sprintf(`histogram_quantile(0.95, sum(rate(traefik_service_request_duration_seconds_bucket{exported_service=~"%s"}[5m])) by (le)) or histogram_quantile(0.95, sum(rate(traefik_service_request_duration_seconds_bucket{service=~"%s"}[5m])) by (le))`, trafikSvc, trafikSvc)
		nginx := fmt.Sprintf(`histogram_quantile(0.95, sum(rate(nginx_ingress_controller_request_duration_seconds_bucket{exported_namespace="%s",ingress="%s"}[5m])) by (le)) or histogram_quantile(0.95, sum(rate(nginx_ingress_controller_request_duration_seconds_bucket{namespace="%s",ingress="%s"}[5m])) by (le))`, targetNS, appId, targetNS, appId)
		query = fmt.Sprintf(`%s or %s`, traefik, nginx)
	case "http_latency_p99":
		trafikSvc := fmt.Sprintf(`%s-%s-.*@kubernetes`, targetNS, appId)
		traefik := fmt.Sprintf(`histogram_quantile(0.99, sum(rate(traefik_service_request_duration_seconds_bucket{exported_service=~"%s"}[5m])) by (le)) or histogram_quantile(0.99, sum(rate(traefik_service_request_duration_seconds_bucket{service=~"%s"}[5m])) by (le))`, trafikSvc, trafikSvc)
		nginx := fmt.Sprintf(`histogram_quantile(0.99, sum(rate(nginx_ingress_controller_request_duration_seconds_bucket{exported_namespace="%s",ingress="%s"}[5m])) by (le)) or histogram_quantile(0.99, sum(rate(nginx_ingress_controller_request_duration_seconds_bucket{namespace="%s",ingress="%s"}[5m])) by (le))`, targetNS, appId, targetNS, appId)
		query = fmt.Sprintf(`%s or %s`, traefik, nginx)
	default:
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("unknown metric: %s. Available: cpu, memory, network_rx, network_tx, restarts, http_rate, http_errors, http_latency_p95, http_latency_p99", metric),
		})
		return
	}

	result, err := h.K8s.QueryPrometheusRange(c.Request.Context(), prometheusURL, query, start, end, step)
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: fmt.Sprintf("prometheus query failed: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"available":  true,
		"metric":     metric,
		"range":      timeRange,
		"start":      start.Unix(),
		"end":        end.Unix(),
		"step":       int(step.Seconds()),
		"resultType": result.ResultType,
		"series":     result.Results,
	})
}

// GetPrometheusStatus returns whether Prometheus is available for this cluster.
func (h *Handler) GetPrometheusStatus(c *gin.Context) {
	prometheusURL := ""
	if cfg, err := h.K8s.GetClusterResource(c.Request.Context(), k8s.VestaConfigGVR, "vesta"); err == nil {
		spec, _, _ := unstructuredNestedMap(cfg.Object, "spec")
		if u, ok := spec["prometheusUrl"].(string); ok && u != "" {
			prometheusURL = u
		}
	}
	if prometheusURL == "" {
		prometheusURL = h.K8s.DiscoverPrometheusURL(c.Request.Context())
	}

	available := prometheusURL != ""
	metrics := []string{}
	httpAvailable := false
	if available {
		metrics = []string{"cpu", "memory", "network_rx", "network_tx", "restarts"}
		// Probe Prometheus to check if ingress metrics exist (Traefik or nginx)
		if h.K8s.QueryPrometheusHasData(c.Request.Context(), prometheusURL, `count(nginx_ingress_controller_requests)`) || h.K8s.QueryPrometheusHasData(c.Request.Context(), prometheusURL, `count(traefik_service_requests_total)`) {
			metrics = append(metrics, "http_rate", "http_errors", "http_latency_p95", "http_latency_p99")
			httpAvailable = true
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"available":        available,
		"prometheusUrl":    prometheusURL,
		"availableMetrics": metrics,
		"httpAvailable":    httpAvailable,
	})
}
