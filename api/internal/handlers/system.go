package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
	"kubernetes.getvesta.sh/api/internal/version"
)

// vestaComponents are the three deployments that make up an installation. Ordered as the
// UI should show them, not as they roll.
var vestaComponents = []string{"api", "operator", "ui"}

// semverPattern is what a release version must look like. Deliberately strict: the
// version reaches an image tag and a Helm --version, so anything that is not an
// immutable release identifier is refused before it gets near either.
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// ReleaseNamespace is where Vesta's own components live. Read from the downward API when
// available so a non-default install still works, falling back to the conventional name.
func ReleaseNamespace() string {
	if ns := strings.TrimSpace(os.Getenv("VESTA_NAMESPACE")); ns != "" {
		return ns
	}
	return vestaSystemNS
}

// ComponentVersion is one Vesta deployment and the image it is actually running.
type ComponentVersion struct {
	Component string `json:"component"`
	Image     string `json:"image"`
	Tag       string `json:"tag"`
	Ready     bool   `json:"ready"`
}

// GetSystemVersion reports what is running.
//
// The image tags are read from the live Deployments rather than from the chart, because
// after an upgrade those are the only honest answer -- the API's own compiled-in version
// is whatever image happened to serve this request, and the chart's appVersion label goes
// stale the moment anything is patched.
func (h *Handler) GetSystemVersion(c *gin.Context) {
	components, err := h.observedComponents(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api":        version.Get(),
		"components": components,
		"namespace":  ReleaseNamespace(),
	})
}

// observedComponents finds Vesta's own deployments by label.
//
// By label and never by name: the deployment is called "<release>-api", so hardcoding
// "vesta-api" breaks for anyone who ran `helm install myvesta`. A component that does not
// resolve to exactly one deployment is reported rather than guessed at.
func (h *Handler) observedComponents(ctx context.Context) ([]ComponentVersion, error) {
	ns := ReleaseNamespace()
	out := make([]ComponentVersion, 0, len(vestaComponents))

	for _, component := range vestaComponents {
		selector := "app.kubernetes.io/part-of=vesta,app.kubernetes.io/component=" + component
		list, err := h.K8s.ListResources(ctx, k8s.DeploymentGVR, ns, selector)
		if err != nil {
			return nil, fmt.Errorf("listing %s deployment: %w", component, err)
		}
		if len(list.Items) != 1 {
			out = append(out, ComponentVersion{Component: component, Image: "", Tag: "unknown"})
			continue
		}

		item := list.Items[0]
		containers, _, _ := unstructuredNestedSlice(item.Object, "spec", "template", "spec", "containers")
		image := ""
		if len(containers) > 0 {
			if m, ok := containers[0].(map[string]interface{}); ok {
				image = getNestedString(m, "image")
			}
		}

		status, _, _ := unstructuredNestedMap(item.Object, "status")
		ready := false
		if status != nil {
			replicas, _ := status["readyReplicas"].(int64)
			if replicas == 0 {
				if f, ok := status["readyReplicas"].(float64); ok {
					replicas = int64(f)
				}
			}
			ready = replicas > 0
		}

		out = append(out, ComponentVersion{
			Component: component,
			Image:     image,
			Tag:       imageTag(image),
			Ready:     ready,
		})
	}
	return out, nil
}

// imageTag returns the tag from a reference, tolerating a registry port in the host.
func imageTag(image string) string {
	if image == "" {
		return "unknown"
	}
	// Split on the last colon, but only if it comes after the last slash -- otherwise
	// "registry:5000/vesta/api" reports "5000/vesta/api" as the tag.
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon > slash {
		return image[colon+1:]
	}
	return "latest"
}

// GetUpdateStatus reports the newest known release against what is running.
func (h *Handler) GetUpdateStatus(c *gin.Context) {
	ctx := c.Request.Context()
	current := version.Version

	latest, _ := h.DB.GetSetting(ctx, db.SettingUpdateLatestVersion)
	checkedAt, _ := h.DB.GetSetting(ctx, db.SettingUpdateCheckedAt)
	enabled := h.DB.GetBoolSetting(ctx, db.SettingUpdateCheckEnabled, true)

	// A development build has no meaningful place in the release ordering, so it never
	// reports an available update -- otherwise every local build would show a banner
	// telling the developer to downgrade to the newest tag.
	available := false
	if enabled && version.IsRelease() && latest != "" {
		available = services.IsNewerVersion(current, latest)
	}

	c.JSON(http.StatusOK, gin.H{
		"current":         current,
		"latest":          latest,
		"updateAvailable": available,
		"checkedAt":       checkedAt,
		"checkEnabled":    enabled,
		"isRelease":       version.IsRelease(),
		"releaseNotesUrl": releaseNotesURL(latest),
	})
}

func releaseNotesURL(v string) string {
	if v == "" {
		return ""
	}
	return "https://github.com/vesta-infra/vesta-kubernetes/releases/tag/v" + v
}

// CheckForUpdates polls the release feed immediately rather than waiting for the timer.
func (h *Handler) CheckForUpdates(c *gin.Context) {
	if !h.DB.GetBoolSetting(c.Request.Context(), db.SettingUpdateCheckEnabled, true) {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: "update checks are disabled on this instance",
		})
		return
	}

	latest, err := services.FetchLatestRelease(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: err.Error()})
		return
	}
	if err := h.DB.RecordUpdateCheck(c.Request.Context(), latest); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	h.GetUpdateStatus(c)
}

// UpdateSettings turns the background check on or off.
func (h *Handler) UpdateSettings(c *gin.Context) {
	var req struct {
		CheckEnabled *bool `json:"checkEnabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	value := "false"
	if *req.CheckEnabled {
		value = "true"
	}
	if err := h.DB.SetSetting(c.Request.Context(), db.SettingUpdateCheckEnabled, value, c.GetString("userId")); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"checkEnabled": *req.CheckEnabled})
	h.auditLog(c, "update_settings", "setting", db.SettingUpdateCheckEnabled, "update checks", "", "",
		map[string]interface{}{"checkEnabled": *req.CheckEnabled})
}

// TriggerUpdate upgrades Vesta itself.
//
// It runs `helm upgrade` in a Job rather than patching the three Deployments, for two
// reasons. Helm stays the source of truth, so the next manual upgrade does not silently
// revert this one -- a direct patch is invisible to the stored release and
// `--reuse-values` would put the old tags straight back. And the Job outlives the API pod,
// which matters because upgrading restarts the very process handling this request.
func (h *Handler) TriggerUpdate(c *gin.Context) {
	var req struct {
		Version string `json:"version" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	target := strings.TrimPrefix(strings.TrimSpace(req.Version), "v")
	// Reject anything that is not an immutable release identifier. "latest" and moving
	// tags are refused outright: imagePullPolicy is IfNotPresent, so a node that already
	// cached that tag would never pull the new image and the "upgrade" would silently do
	// nothing on some nodes and not others.
	if !semverPattern.MatchString(target) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("%q is not a release version; expected something like 0.6.4", req.Version),
		})
		return
	}

	if target == version.Version {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: fmt.Sprintf("already running %s", target),
		})
		return
	}

	// Pointing Vesta's own deployments at an arbitrary version is close to cluster-admin,
	// so it costs a fresh proof of identity the same way removing a second factor does.
	userID := c.GetString("userId")
	if !h.requireReauth(c, userID) {
		return
	}

	// Confirm the release exists before creating anything. A typo'd version would
	// otherwise produce a Job that fails deep inside helm with a registry error.
	known, err := services.ReleaseExists(c.Request.Context(), target)
	if err == nil && !known {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("no published release %s", target),
		})
		return
	}

	jobName, err := h.Updater.Start(c.Request.Context(), services.UpdateRequest{
		Version:     target,
		Namespace:   ReleaseNamespace(),
		TriggeredBy: userID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// 202: the work outlives this request, and this process is one of the things it
	// replaces. The client must poll rather than wait for completion here.
	c.JSON(http.StatusAccepted, gin.H{
		"status":  "started",
		"version": target,
		"job":     jobName,
		"note":    "the API will restart during the upgrade; poll /system/update/status to follow it",
	})

	h.auditLog(c, "system_update", "system", target, "vesta", "", "",
		map[string]interface{}{"from": version.Version, "to": target, "job": jobName})
}

// GetUpdateProgress reports on the most recent upgrade attempt.
func (h *Handler) GetUpdateProgress(c *gin.Context) {
	history, err := h.DB.ListUpdateHistory(c.Request.Context(), 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	sort.SliceStable(history, func(i, j int) bool {
		return history[i].StartedAt.After(history[j].StartedAt)
	})

	var latest interface{}
	if len(history) > 0 {
		latest = history[0]
	}
	c.JSON(http.StatusOK, gin.H{"current": latest, "history": history})
}
