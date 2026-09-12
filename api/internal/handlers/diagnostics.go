package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// A "Failed" app used to be a dead end: the phase said something was wrong but
// not what, and finding out meant kubectl access to the cluster. This endpoint
// assembles everything Kubernetes already knows about why an app is unhealthy --
// container states, exit codes, scheduling failures, warning events -- and pairs
// each finding with the action that usually resolves it.

type diagnosticIssue struct {
	Severity  string `json:"severity"` // error | warning | info
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Pod       string `json:"pod,omitempty"`
	Container string `json:"container,omitempty"`
	Restarts  int32  `json:"restarts,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

type diagnosticEvent struct {
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Object   string `json:"object"`
	Count    int32  `json:"count"`
	LastSeen string `json:"lastSeen"`
}

type environmentDiagnostics struct {
	Environment string            `json:"environment"`
	Namespace   string            `json:"namespace"`
	ReadyPods   int               `json:"readyPods"`
	TotalPods   int               `json:"totalPods"`
	Issues      []diagnosticIssue `json:"issues"`
	Events      []diagnosticEvent `json:"events"`
}

// hints map a Kubernetes reason to the thing to actually go and check. Without
// these the reason is still jargon to anyone who doesn't run clusters daily.
var reasonHints = map[string]string{
	"ImagePullBackOff":           "Check the image repository and tag exist, and that a registry credential is attached to this app or project.",
	"ErrImagePull":               "Check the image repository and tag exist, and that a registry credential is attached to this app or project.",
	"InvalidImageName":           "The image reference is malformed. Check the repository and tag on the app's Image settings.",
	"CreateContainerConfigError": "A referenced secret or config key is missing. Check the app's bound secrets for this environment.",
	"CreateContainerError":       "The container could not be created. Check the start command, arguments, and mounted volumes.",
	"CrashLoopBackOff":           "The container starts then exits. Check the Logs tab for the crash output, and verify the start command and required environment variables.",
	"RunContainerError":          "The container could not run. Check the start command and that it is executable in the image.",
	"StartError":                 "The container failed to start. Check the start command and the image entrypoint.",
	"OOMKilled":                  "The container exceeded its memory limit. Raise the pod size for this environment or reduce memory use.",
	"Unschedulable":              "No node can fit this pod. Reduce the pod size or replica count, or add cluster capacity.",
	"FailedScheduling":           "No node can fit this pod. Reduce the pod size or replica count, or add cluster capacity.",
	"FailedMount":                "A volume or secret could not be mounted. Check the app's volumes and that referenced secrets exist in this environment.",
	"FailedCreate":               "The replica set could not create pods. The message usually names a quota or admission policy that blocked it.",
	"ProgressDeadlineExceeded":   "The rollout did not complete in time. The new pods are likely failing their readiness check.",
	"ContainerNotReady":          "The container runs but never passes its readiness check. Verify the health check path, port, and initial delay.",
	"InvalidSpec":                "The generated Kubernetes object was rejected. The message names the exact field; correct it in the app's settings.",
	"QuotaExceeded":              "The namespace resource quota is exhausted. Lower the pod size or replicas, or raise the quota.",
	"Forbidden":                  "The platform lacks permission for this operation. Check the Vesta service account's RBAC.",
	"MissingDependency":          "Something the app references does not exist yet, such as a secret or environment.",
	"BackOff":                    "Kubernetes is backing off after repeated failures. The earlier events explain the original cause.",
}

func hintFor(reason string) string { return reasonHints[reason] }

// GetAppDiagnostics explains why an app is not healthy, per environment.
func (h *Handler) GetAppDiagnostics(c *gin.Context) {
	appID := c.Param("appId")

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(app.Object, "spec")
	status, _, _ := unstructuredNestedMap(app.Object, "status")
	project := getNestedString(spec, "project")

	phase := getNestedString(status, "phase")
	if phase == "" {
		phase = "Pending"
	}
	statusReason := getNestedString(status, "reason")

	// Restrict to one environment when asked, so the app detail page can scope the
	// panel to whatever environment the user is looking at.
	envFilter := c.Query("environment")

	envs, _, _ := unstructuredNestedSlice(app.Object, "spec", "environments")
	results := make([]environmentDiagnostics, 0, len(envs))

	for _, rawEnv := range envs {
		env, ok := rawEnv.(map[string]interface{})
		if !ok {
			continue
		}
		envName := getNestedString(env, "name")
		if envName == "" || (envFilter != "" && envName != envFilter) {
			continue
		}

		namespace := fmt.Sprintf("%s-%s", project, envName)
		diag := environmentDiagnostics{
			Environment: envName,
			Namespace:   namespace,
			Issues:      []diagnosticIssue{},
			Events:      []diagnosticEvent{},
		}

		pods, err := h.K8s.ListPods(c.Request.Context(), namespace, fmt.Sprintf("kubernetes.getvesta.sh/app=%s", appID))
		if err == nil {
			podNames := map[string]bool{}
			for _, pod := range pods {
				if _, isCronPod := pod.Labels["kubernetes.getvesta.sh/cronjob"]; isCronPod {
					continue
				}
				podNames[pod.Name] = true
				diag.TotalPods++
				if podIsReady(pod) {
					diag.ReadyPods++
				}
				diag.Issues = append(diag.Issues, diagnosePodForAPI(pod)...)
			}

			// Warning events explain the failures that never produce a container
			// status at all -- failed scheduling, failed mounts, quota rejections.
			if events, err := h.K8s.ListEvents(c.Request.Context(), namespace); err == nil {
				diag.Events = relevantEvents(events, appID, podNames)
			}
		}

		results = append(results, diag)
	}

	c.JSON(http.StatusOK, gin.H{
		"app":          appID,
		"phase":        phase,
		"reason":       statusReason,
		"message":      getNestedString(status, "message"),
		"hint":         hintFor(statusReason),
		"environments": results,
		"checkedAt":    time.Now().UTC().Format(time.RFC3339),
	})
}

func podIsReady(pod corev1.Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

// diagnosePodForAPI mirrors the operator's diagnosis but keeps the structure the
// UI needs: which pod, which container, and what to do about it.
func diagnosePodForAPI(pod corev1.Pod) []diagnosticIssue {
	var issues []diagnosticIssue

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason != "" {
			issues = append(issues, diagnosticIssue{
				Severity: "error",
				Reason:   cond.Reason,
				Message:  firstLineOf(cond.Message),
				Pod:      pod.Name,
				Hint:     hintFor(cond.Reason),
			})
		}
	}

	statuses := append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)

	for _, cs := range statuses {
		switch {
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
			reason := cs.State.Waiting.Reason
			// These two are normal startup states, not problems.
			if reason == "ContainerCreating" || reason == "PodInitializing" {
				continue
			}
			message := firstLineOf(cs.State.Waiting.Message)
			hintReason := reason
			// A crash loop's own message says nothing; the previous exit does.
			if t := cs.LastTerminationState.Terminated; t != nil {
				if detail := describeExit(t); detail != "" {
					message = detail
				}
				if t.Reason == "OOMKilled" {
					hintReason = "OOMKilled"
				}
			}
			issues = append(issues, diagnosticIssue{
				Severity:  "error",
				Reason:    reason,
				Message:   message,
				Pod:       pod.Name,
				Container: cs.Name,
				Restarts:  cs.RestartCount,
				Hint:      hintFor(hintReason),
			})

		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0:
			t := cs.State.Terminated
			reason := t.Reason
			if reason == "" {
				reason = "ContainerTerminated"
			}
			issues = append(issues, diagnosticIssue{
				Severity:  "error",
				Reason:    reason,
				Message:   describeExit(t),
				Pod:       pod.Name,
				Container: cs.Name,
				Restarts:  cs.RestartCount,
				Hint:      hintFor(reason),
			})

		case cs.State.Running != nil && !cs.Ready:
			issues = append(issues, diagnosticIssue{
				Severity:  "warning",
				Reason:    "ContainerNotReady",
				Message:   "Running but not passing its readiness check",
				Pod:       pod.Name,
				Container: cs.Name,
				Restarts:  cs.RestartCount,
				Hint:      hintFor("ContainerNotReady"),
			})

		case cs.RestartCount > 0 && cs.Ready:
			// Recovered, but the restarts are worth knowing about.
			issues = append(issues, diagnosticIssue{
				Severity:  "info",
				Reason:    "ContainerRestarted",
				Message:   fmt.Sprintf("Recovered after %d restart(s)", cs.RestartCount),
				Pod:       pod.Name,
				Container: cs.Name,
				Restarts:  cs.RestartCount,
			})
		}
	}

	return issues
}

func describeExit(t *corev1.ContainerStateTerminated) string {
	parts := []string{}
	if t.Reason != "" {
		parts = append(parts, t.Reason)
	}
	switch {
	case t.Reason == "OOMKilled":
		parts = append(parts, "exceeded its memory limit")
	case t.ExitCode == 137:
		parts = append(parts, "exit 137, usually the memory limit")
	case t.ExitCode != 0:
		parts = append(parts, fmt.Sprintf("exit code %d", t.ExitCode))
	}
	if detail := firstLineOf(t.Message); detail != "" {
		parts = append(parts, detail)
	}
	return strings.Join(parts, ", ")
}

// relevantEvents keeps warning events belonging to this app's own objects, newest
// first, capped so one noisy pod can't crowd out everything else.
func relevantEvents(events []corev1.Event, appID string, podNames map[string]bool) []diagnosticEvent {
	const maxEvents = 15
	out := []diagnosticEvent{}

	for _, e := range events {
		if e.Type != corev1.EventTypeWarning {
			continue
		}
		name := e.InvolvedObject.Name
		belongs := podNames[name] || name == appID || strings.HasPrefix(name, appID+"-")
		if !belongs {
			continue
		}

		count := e.Count
		if e.Series != nil && e.Series.Count > count {
			count = e.Series.Count
		}
		if count == 0 {
			count = 1
		}

		out = append(out, diagnosticEvent{
			Type:     e.Type,
			Reason:   e.Reason,
			Message:  firstLineOf(e.Message),
			Object:   fmt.Sprintf("%s/%s", strings.ToLower(e.InvolvedObject.Kind), name),
			Count:    count,
			LastSeen: k8s.EventTime(e).UTC().Format(time.RFC3339),
		})
		if len(out) >= maxEvents {
			break
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

// firstLineOf keeps messages to a single line; Kubernetes messages are often long
// multi-line dumps that wreck a compact panel.
func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\n\r"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	const max = 300
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}
