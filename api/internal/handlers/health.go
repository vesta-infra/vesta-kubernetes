package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
)

// GetHealthDashboard returns an aggregated health overview of all apps.
func (h *Handler) GetHealthDashboard(c *gin.Context) {
	projectFilter := c.Query("projectId")

	appsList, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": fmt.Sprintf("failed to list apps: %v", err)})
		return
	}

	type appHealth struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Project     string `json:"project"`
		Phase       string `json:"phase"`
		Replicas    int64  `json:"replicas"`
		ReadyPods   int    `json:"readyPods"`
		TotalPods   int    `json:"totalPods"`
		Restarts    int32  `json:"restarts"`
		URL         string `json:"url,omitempty"`
		LastDeploy  string `json:"lastDeployedAt,omitempty"`
		SleepMode   bool   `json:"sleepMode"`
	}

	var results []appHealth
	statusCounts := map[string]int{"Running": 0, "Failed": 0, "Pending": 0, "Sleeping": 0}

	for _, app := range appsList.Items {
		spec, _, _ := unstructuredNestedMap(app.Object, "spec")
		status, _, _ := unstructuredNestedMap(app.Object, "status")

		project := getNestedString(spec, "project")
		if projectFilter != "" && project != projectFilter {
			continue
		}

		phase := getNestedString(status, "phase")
		if phase == "" {
			phase = "Pending"
		}
		statusCounts[phase]++

		sleepSpec, _, _ := unstructuredNestedMap(spec, "sleep")
		sleepEnabled := false
		if sleepSpec != nil {
			if v, ok := sleepSpec["enabled"].(bool); ok {
				sleepEnabled = v
			}
		}

		replicas := int64(1)
		if r, ok := status["replicas"]; ok {
			if v, ok := r.(int64); ok {
				replicas = v
			}
		}

		ah := appHealth{
			ID:         app.GetName(),
			Name:       app.GetName(),
			Project:    project,
			Phase:      phase,
			Replicas:   replicas,
			URL:        getNestedString(status, "url"),
			LastDeploy: getNestedString(status, "lastDeployedAt"),
			SleepMode:  sleepEnabled,
		}

		// Try to get pod information for this app
		envs, _, _ := unstructuredNestedSlice(app.Object, "spec", "environments")
		if len(envs) > 0 {
			firstEnv, ok := envs[0].(map[string]interface{})
			if ok {
				envName := ""
				if n, ok := firstEnv["name"].(string); ok {
					envName = n
				}
				if envName != "" {
					targetNS := fmt.Sprintf("%s-%s", project, envName)
					labelSelector := fmt.Sprintf("kubernetes.getvesta.sh/app=%s", app.GetName())
					pods, err := h.K8s.ListPods(c.Request.Context(), targetNS, labelSelector)
					if err == nil {
						ah.TotalPods = len(pods)
						for _, pod := range pods {
							if pod.Status.Phase == "Running" {
								ready := true
								for _, cs := range pod.Status.ContainerStatuses {
									if !cs.Ready {
										ready = false
									}
									ah.Restarts += cs.RestartCount
								}
								if ready {
									ah.ReadyPods++
								}
							}
						}
					}
				}
			}
		}

		results = append(results, ah)
	}

	c.JSON(http.StatusOK, gin.H{
		"apps":    results,
		"total":   len(results),
		"summary": statusCounts,
	})
}
