package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// TriggerBuild starts a new build for an app.
func (h *Handler) TriggerBuild(c *gin.Context) {
	appId := c.Param("appId")
	var req models.TriggerBuildRequest
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

	gitSpec, _, _ := unstructuredNestedMap(spec, "git")
	if gitSpec == nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: "app has no git configuration; set spec.git via PUT /apps/:appId first",
		})
		return
	}

	buildSpec, _, _ := unstructuredNestedMap(spec, "build")
	strategy := "dockerfile"
	dockerfile := "Dockerfile"
	if buildSpec != nil {
		if s, ok := buildSpec["strategy"].(string); ok && s != "" && s != "image" {
			strategy = s
		}
		if d, ok := buildSpec["dockerfile"].(string); ok && d != "" {
			dockerfile = d
		}
	}

	imageSpec, _, _ := unstructuredNestedMap(spec, "image")
	if imageSpec == nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: "app has no image configuration; set spec.image.repository via PUT /apps/:appId first",
		})
		return
	}

	repository, _ := gitSpec["repository"].(string)
	branch, _ := gitSpec["branch"].(string)
	if req.Branch != "" {
		branch = req.Branch
	}
	if branch == "" {
		branch = "main"
	}

	commitSHA := req.CommitSHA
	imageRepo, _ := imageSpec["repository"].(string)

	// Build tag from commit SHA or timestamp
	tag := commitSHA
	if len(tag) > 8 {
		tag = tag[:8]
	}
	if tag == "" {
		tag = fmt.Sprintf("build-%d", time.Now().Unix())
	}

	imageDest := fmt.Sprintf("%s:%s", imageRepo, tag)

	// Find a registry secret from imagePullSecrets
	registrySecret := ""
	if pullSecrets, ok := imageSpec["imagePullSecrets"].([]interface{}); ok && len(pullSecrets) > 0 {
		if first, ok := pullSecrets[0].(map[string]interface{}); ok {
			registrySecret, _ = first["name"].(string)
		}
	}
	// Also check per-environment imagePullSecrets
	if registrySecret == "" {
		envs, _ := spec["environments"].([]interface{})
		for _, e := range envs {
			envMap, _ := e.(map[string]interface{})
			if envName, _ := envMap["name"].(string); envName == req.Environment {
				if ps, ok := envMap["imagePullSecrets"].([]interface{}); ok && len(ps) > 0 {
					if first, ok := ps[0].(map[string]interface{}); ok {
						registrySecret, _ = first["name"].(string)
					}
				}
			}
		}
	}

	triggeredBy := "api-token"
	if uid := c.GetString("userId"); uid != "" {
		triggeredBy = fmt.Sprintf("user:%s", uid)
	}

	buildReq := services.BuildRequest{
		AppID:          appId,
		ProjectID:      project,
		Environment:    req.Environment,
		Strategy:       strategy,
		Repository:     repository,
		Branch:         branch,
		CommitSHA:      commitSHA,
		Dockerfile:     dockerfile,
		ImageDest:      imageDest,
		RegistrySecret: registrySecret,
		TriggeredBy:    triggeredBy,
	}

	buildID, err := h.Builder.TriggerBuild(c.Request.Context(), buildReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:    500,
			Message: fmt.Sprintf("failed to trigger build: %v", err),
		})
		return
	}

	h.auditLog(c, "build", "app", appId, appId, project, req.Environment,
		map[string]interface{}{"strategy": strategy, "commitSHA": commitSHA, "image": imageDest, "reason": req.Reason})

	c.JSON(http.StatusAccepted, models.BuildResponse{
		ID:          buildID,
		AppID:       appId,
		ProjectID:   project,
		Environment: req.Environment,
		Status:      "pending",
		Strategy:    strategy,
		CommitSHA:   commitSHA,
		Branch:      branch,
		Repository:  repository,
		Image:       imageDest,
		TriggeredBy: triggeredBy,
		LogsURL:     fmt.Sprintf("/api/v1/apps/%s/builds/%s/logs", appId, buildID),
		CreatedAt:   models.NowRFC3339(),
	})
}

// ListBuilds returns the build history for an app.
func (h *Handler) ListBuilds(c *gin.Context) {
	appId := c.Param("appId")

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	builds, total, err := h.DB.ListBuilds(c.Request.Context(), db.BuildFilter{
		AppID:  appId,
		Status: c.Query("status"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]models.BuildResponse, len(builds))
	for i, b := range builds {
		items[i] = buildToResponse(b, appId)
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: total})
}

// GetBuild returns a single build's status.
func (h *Handler) GetBuild(c *gin.Context) {
	appId := c.Param("appId")
	buildId := c.Param("buildId")

	build, err := h.DB.GetBuild(c.Request.Context(), buildId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "build not found"})
		return
	}

	c.JSON(http.StatusOK, buildToResponse(*build, appId))
}

// GetBuildLogs returns or streams the logs for a build's Kubernetes Job.
func (h *Handler) GetBuildLogs(c *gin.Context) {
	buildId := c.Param("buildId")

	build, err := h.DB.GetBuild(c.Request.Context(), buildId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "build not found"})
		return
	}

	if build.JobName == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "no build job found"})
		return
	}

	follow := c.Query("follow") == "true"
	tailLines := int64(500)
	if tl := c.Query("tail"); tl != "" {
		if n, err := strconv.ParseInt(tl, 10, 64); err == nil && n > 0 {
			tailLines = n
			if tailLines > 10000 {
				tailLines = 10000
			}
		}
	}

	// Find the build pod by job-name label
	labelSelector := fmt.Sprintf("job-name=%s", build.JobName)
	pods, err := h.K8s.ListPods(c.Request.Context(), vestaSystemNS, labelSelector)
	if err != nil || len(pods) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"buildId": buildId,
			"status":  build.Status,
			"logs":    "",
			"message": "build pod not found — it may have been cleaned up",
		})
		return
	}

	podName := pods[0].Name

	if follow {
		// SSE stream
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")
		c.Writer.Flush()

		stream, err := h.K8s.StreamPodLogs(c.Request.Context(), vestaSystemNS, podName, "build", tailLines)
		if err != nil {
			fmt.Fprintf(c.Writer, "data: {\"error\": \"%s\"}\n\n", err.Error())
			c.Writer.Flush()
			return
		}
		defer stream.Close()

		scanner := bufio.NewScanner(stream)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			c.Writer.Flush()
		}

		// Send completion event
		b, _ := h.DB.GetBuild(c.Request.Context(), buildId)
		status := "unknown"
		if b != nil {
			status = b.Status
		}
		fmt.Fprintf(c.Writer, "event: done\ndata: {\"status\":\"%s\"}\n\n", status)
		c.Writer.Flush()

		return
	}

	// Non-streaming: return collected logs
	logs, err := h.K8s.GetPodLogs(c.Request.Context(), vestaSystemNS, podName, "build", tailLines, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:    500,
			Message: fmt.Sprintf("failed to get build logs: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"buildId": buildId,
		"status":  build.Status,
		"logs":    logs,
	})
}

// CancelBuild cancels a running build by deleting the Job.
func (h *Handler) CancelBuild(c *gin.Context) {
	buildId := c.Param("buildId")

	build, err := h.DB.GetBuild(c.Request.Context(), buildId)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "build not found"})
		return
	}

	if build.Status != "pending" && build.Status != "running" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("cannot cancel build in status %q", build.Status),
		})
		return
	}

	// Delete the Job (propagation: Background will also delete pods)
	propagation := metav1.DeletePropagationBackground
	err = h.K8s.Clientset.BatchV1().Jobs(vestaSystemNS).Delete(
		c.Request.Context(), build.JobName, metav1.DeleteOptions{PropagationPolicy: &propagation})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code:    500,
			Message: fmt.Sprintf("failed to cancel build job: %v", err),
		})
		return
	}

	h.DB.UpdateBuildStatus(c.Request.Context(), buildId, "cancelled", "cancelled by user")

	c.JSON(http.StatusOK, gin.H{"status": "cancelled", "buildId": buildId})
}

func buildToResponse(b db.Build, appId string) models.BuildResponse {
	resp := models.BuildResponse{
		ID:          b.ID,
		AppID:       b.AppID,
		ProjectID:   b.ProjectID,
		Environment: b.Environment,
		Status:      b.Status,
		Strategy:    b.Strategy,
		CommitSHA:   b.CommitSHA,
		Branch:      b.Branch,
		Repository:  b.Repository,
		Image:       b.Image,
		TriggeredBy: b.TriggeredBy,
		DurationMs:  b.DurationMs,
		Error:       b.ErrorMessage,
		LogsURL:     fmt.Sprintf("/api/v1/apps/%s/builds/%s/logs", appId, b.ID),
		CreatedAt:   b.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if b.StartedAt != nil {
		s := b.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.StartedAt = &s
	}
	if b.FinishedAt != nil {
		s := b.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.FinishedAt = &s
	}
	return resp
}
