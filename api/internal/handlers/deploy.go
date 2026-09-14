package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
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

		// Retry loop to handle optimistic concurrency conflicts on the VestaApp CR.
		const maxRetries = 5
		var updateErr error
		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				// Re-fetch the latest version of the resource
				existing, err = h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
				if err != nil {
					c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to re-fetch app: %v", err)})
					return
				}
				spec, _, _ = unstructuredNestedMap(existing.Object, "spec")
				imageSpec, _, _ = unstructuredNestedMap(spec, "image")
			}

			// Update the per-environment image tag on the VestaApp CRD.
			// The operator reads env.image.tag (falling back to spec.image) per-environment.
			environments, _, _ := unstructuredNestedSlice(spec, "environments")
			updated := false
			for i, raw := range environments {
				envMap, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				if getNestedString(envMap, "name") == req.Environment {
					envImage, _ := envMap["image"].(map[string]interface{})
					if envImage == nil {
						envImage = map[string]interface{}{}
					}
					envImage["tag"] = req.Tag
					envMap["image"] = envImage
					environments[i] = envMap
					updated = true
					break
				}
			}
			if !updated {
				// Fallback: patch global image tag for apps without per-env entries
				spec["image"] = map[string]interface{}{
					"repository": repo,
					"tag":        req.Tag,
				}
			} else {
				// Only the target environment's tag changes. spec.image.tag is the
				// default for environments that have no tag of their own, so writing
				// it here would deploy this tag to every other such environment.
				spec["environments"] = environments
			}
			existing.Object["spec"] = spec

			// Store the target environment as an annotation for the operator to record in deployment history
			metadata, _ := existing.Object["metadata"].(map[string]interface{})
			annotations, _ := metadata["annotations"].(map[string]interface{})
			if annotations == nil {
				annotations = map[string]interface{}{}
			}
			annotations["vesta.sh/last-deploy-environment"] = req.Environment
			metadata["annotations"] = annotations
			existing.Object["metadata"] = metadata

			_, updateErr = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, existing)
			if updateErr == nil {
				break
			}
			if !k8serrors.IsConflict(updateErr) {
				break
			}
		}
		if updateErr != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to update app image tag: %v", updateErr)})
			return
		}

		h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
			Type:        services.EventDeployStarted,
			ProjectID:   project,
			AppID:       appId,
			Environment: req.Environment,
			Image:       targetImage,
			TriggeredBy: triggeredBy,
			Message:     fmt.Sprintf("Deploying %s to %s", targetImage, req.Environment),
		})

		c.JSON(http.StatusAccepted, models.DeployResponse{
			ID:          deployId,
			AppID:       appId,
			Status:      "deploying",
			Image:       targetImage,
			TriggeredBy: triggeredBy,
			TriggeredAt: now,
			StatusURL:   fmt.Sprintf("/api/v1/apps/%s/deployments/%s", appId, deployId),
		})

		h.auditLog(c, "deploy", "app", appId, appId, project, req.Environment,
			map[string]interface{}{"image": targetImage, "tag": req.Tag, "reason": req.Reason})

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

		h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
			Type:        services.EventDeployStarted,
			ProjectID:   project,
			AppID:       appId,
			Environment: req.Environment,
			TriggeredBy: triggeredBy,
			Message:     fmt.Sprintf("Redeploying %s in %s", appId, req.Environment),
		})

		c.JSON(http.StatusAccepted, models.DeployResponse{
			ID:          deployId,
			AppID:       appId,
			Status:      "deploying",
			TriggeredBy: triggeredBy,
			TriggeredAt: now,
			StatusURL:   fmt.Sprintf("/api/v1/apps/%s/deployments/%s", appId, deployId),
		})

		h.auditLog(c, "redeploy", "app", appId, appId, project, req.Environment, nil)

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

	status, _, _ := unstructuredNestedMap(existing.Object, "status")
	history, _ := status["deploymentHistory"].([]interface{})

	// History records are per-environment. Only roll back to a version that belongs
	// to the target environment, otherwise a version from another environment would
	// be deployed here. Legacy records carry no environment and stay eligible.
	var targetImage string
	var wrongEnv string
	for _, entry := range history {
		record, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		ver, _ := record["version"].(float64)
		if int(ver) != req.Version {
			continue
		}
		recEnv, _ := record["environment"].(string)
		if recEnv != "" && recEnv != req.Environment {
			wrongEnv = recEnv
			break
		}
		targetImage, _ = record["image"].(string)
		break
	}

	if targetImage == "" {
		msg := fmt.Sprintf("deployment version %d not found in history", req.Version)
		if wrongEnv != "" {
			msg = fmt.Sprintf("deployment version %d belongs to environment %q, not %q", req.Version, wrongEnv, req.Environment)
		}
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: msg})
		return
	}

	// Update the per-environment image tag — the operator will reconcile
	rollbackTag := targetImage
	if idx := strings.LastIndex(targetImage, ":"); idx >= 0 {
		rollbackTag = targetImage[idx+1:]
	}

	environments, _, _ := unstructuredNestedSlice(spec, "environments")
	updated := false
	for i, raw := range environments {
		envMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if getNestedString(envMap, "name") == req.Environment {
			envImage, _ := envMap["image"].(map[string]interface{})
			if envImage == nil {
				envImage = map[string]interface{}{}
			}
			envImage["tag"] = rollbackTag
			envMap["image"] = envImage
			environments[i] = envMap
			updated = true
			break
		}
	}
	if !updated {
		imageSpec, _, _ := unstructuredNestedMap(spec, "image")
		if imageSpec == nil {
			imageSpec = map[string]interface{}{}
		}
		imageSpec["tag"] = rollbackTag
		spec["image"] = imageSpec
	} else {
		spec["environments"] = environments
	}
	existing.Object["spec"] = spec

	// Store the target environment as an annotation for the operator to record in deployment history
	metadata, _ := existing.Object["metadata"].(map[string]interface{})
	annotations, _ := metadata["annotations"].(map[string]interface{})
	if annotations == nil {
		annotations = map[string]interface{}{}
	}
	annotations["vesta.sh/last-deploy-environment"] = req.Environment
	metadata["annotations"] = annotations
	existing.Object["metadata"] = metadata

	_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, existing)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to rollback app image tag: %v", err)})
		return
	}

	h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
		Type:        services.EventDeployStarted,
		ProjectID:   project,
		AppID:       appId,
		Environment: req.Environment,
		Image:       targetImage,
		TriggeredBy: c.GetString("userId"),
		Message:     fmt.Sprintf("Rolling back %s to version %d (%s)", appId, req.Version, targetImage),
	})

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

	h.auditLog(c, "rollback", "app", appId, appId, project, req.Environment,
		map[string]interface{}{"version": req.Version, "image": targetImage, "reason": req.Reason})
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

	h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
		Type:        services.EventAppRestarted,
		ProjectID:   project,
		AppID:       appId,
		Environment: req.Environment,
		TriggeredBy: c.GetString("userId"),
		Message:     fmt.Sprintf("Restarting %s in %s", appId, req.Environment),
	})

	c.JSON(http.StatusAccepted, gin.H{"status": "restarting", "environment": req.Environment})

	h.auditLog(c, "restart", "app", appId, appId, project, req.Environment, nil)
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

	// Resolve project for notification
	if existing, e := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId); e == nil {
		spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
		project := getNestedString(spec, "project")
		h.Notifier.Send(c.Request.Context(), services.NotificationEvent{
			Type:        services.EventAppScaled,
			ProjectID:   project,
			AppID:       appId,
			TriggeredBy: c.GetString("userId"),
			Message:     fmt.Sprintf("Scaled %s to %d replicas", appId, req.Replicas),
		})
	}

	c.JSON(http.StatusOK, gin.H{"replicas": req.Replicas})

	h.auditLog(c, "scale", "app", appId, appId, "", "",
		map[string]interface{}{"replicas": req.Replicas})
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

// patchLifecycle asks the operator for a lifecycle change by patching spec.desiredState.
//
// The four handlers below used to patch status.phase instead. VestaApp declares a status
// subresource, so a patch to the main resource discards the status stanza without error:
// sleep deadlocked (the operator zeroed replicas only once the phase read "Sleeping", and
// the phase read "Sleeping" only once replicas were already zero) and stop did nothing at
// all. The instruction belongs in the spec; the operator alone owns the phase.
func (h *Handler) patchLifecycle(c *gin.Context, spec map[string]interface{}, verb, action, result string) {
	appId := c.Param("appId")

	patchBytes, _ := json.Marshal(map[string]interface{}{"spec": spec})
	if _, err := h.K8s.PatchResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId, patchBytes); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code: 500, Message: fmt.Sprintf("failed to %s app: %v", verb, err)})
		return
	}

	h.auditLog(c, action, "app", appId, appId, "", "", nil)
	c.JSON(http.StatusOK, gin.H{"status": result, "app": appId})
}

// SleepApp puts an app to sleep (scales to zero).
func (h *Handler) SleepApp(c *gin.Context) {
	h.patchLifecycle(c, map[string]interface{}{
		"desiredState": "sleeping",
		// desiredState is what actually holds the app at zero. spec.sleep stays the
		// policy flag -- set here so an app slept by hand is also marked eligible for
		// scale-to-zero, which is what the earlier behaviour implied and what the
		// inactivity sweeper will read.
		"sleep": map[string]interface{}{"enabled": true},
	}, "sleep", "app.slept", "sleeping")
}

// WakeApp wakes an app from sleep.
func (h *Handler) WakeApp(c *gin.Context) {
	// Tell the sweeper, or it may decide on its next pass that this app has seen no traffic
	// and sleep it straight back -- the request that woke it is usually the only traffic in
	// the window.
	if h.Sleep != nil {
		h.Sleep.NoteWoken(c.Param("appId"))
	}

	h.patchLifecycle(c, map[string]interface{}{
		"desiredState": "running",
		// Clearing the policy as well, matching the previous behaviour: waking by hand
		// opts the app out of scale-to-zero until it is turned back on.
		"sleep": map[string]interface{}{"enabled": false},
	}, "wake", "app.woken", "waking")
}

// StopApp stops an app by scaling to zero replicas.
func (h *Handler) StopApp(c *gin.Context) {
	h.patchLifecycle(c, map[string]interface{}{
		"desiredState": "stopped",
	}, "stop", "app.stopped", "stopped")
}

// StartApp starts a stopped app by restoring its configured replicas.
//
// Unlike WakeApp this leaves spec.sleep alone: stopping is unrelated to the sleep policy,
// and starting an app should not silently disable scale-to-zero for it.
func (h *Handler) StartApp(c *gin.Context) {
	h.patchLifecycle(c, map[string]interface{}{
		"desiredState": "running",
	}, "start", "app.started", "starting")
}

// TriggerCronJob manually triggers a cronjob by creating a one-off Job.
func (h *Handler) TriggerCronJob(c *gin.Context) {
	appId := c.Param("appId")
	cronJobName := c.Param("name")

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
	targetNS := fmt.Sprintf("%s-%s", project, req.Environment)

	fullCronJobName := fmt.Sprintf("%s-%s", appId, cronJobName)
	jobName, err := h.K8s.TriggerCronJob(c.Request.Context(), targetNS, fullCronJobName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to trigger cronjob: %v", err)})
		return
	}

	h.auditLog(c, "trigger_cronjob", "cronjob", fullCronJobName, cronJobName, project, req.Environment,
		map[string]interface{}{"job": jobName})

	c.JSON(http.StatusAccepted, gin.H{"status": "triggered", "job": jobName, "cronjob": cronJobName})
}

// GetCronJobStatuses returns status info for all cronjobs of an app.
func (h *Handler) GetCronJobStatuses(c *gin.Context) {
	appId := c.Param("appId")
	env := c.Query("environment")

	existing, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(existing.Object, "spec")
	project := getNestedString(spec, "project")

	environments := []string{}
	if env != "" {
		environments = append(environments, env)
	} else {
		rawEnvs, _, _ := unstructuredNestedSlice(existing.Object, "spec", "environments")
		for _, e := range rawEnvs {
			switch v := e.(type) {
			case map[string]interface{}:
				if name, _ := v["name"].(string); name != "" {
					environments = append(environments, name)
				}
			case string:
				environments = append(environments, v)
			}
		}
	}

	var allStatuses []map[string]interface{}
	for _, envName := range environments {
		targetNS := fmt.Sprintf("%s-%s", project, envName)
		statuses, err := h.K8s.GetCronJobStatuses(c.Request.Context(), targetNS, fmt.Sprintf("app.kubernetes.io/name=%s", appId))
		if err != nil {
			continue
		}
		for _, s := range statuses {
			allStatuses = append(allStatuses, map[string]interface{}{
				"environment":        envName,
				"name":               s.Name,
				"schedule":           s.Schedule,
				"lastScheduleTime":   s.LastScheduleTime,
				"lastSuccessfulTime": s.LastSuccessfulTime,
				"active":             s.Active,
				"runCount":           s.RunCount,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"items": allStatuses})
}
