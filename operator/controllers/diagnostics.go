package controllers

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// A failing app used to surface as the bare word "Failed", with the actual cause
// only visible in operator logs or by running kubectl by hand. The helpers here
// turn what Kubernetes already knows -- container waiting reasons, exit codes,
// scheduling conditions, reconcile errors -- into a short reason token plus a
// message naming the specific pod and container, which the API and UI then show.

// appIssue is one identified problem, ranked so the most explanatory one wins
// when several are present.
type appIssue struct {
	Reason   string
	Message  string
	Severity int // higher wins
}

// Severity ranks: a container that cannot start explains more than a pod that
// merely has not finished starting.
const (
	sevProgressing = 10
	sevNotReady    = 20
	sevScheduling  = 30
	sevCrash       = 40
	sevImage       = 50
	sevConfig      = 60
	sevReconcile   = 70
)

// terminalWaitingReasons are container waiting reasons that will not resolve on
// their own -- they need a spec or cluster change.
var waitingReasonSeverity = map[string]int{
	"ErrImagePull":               sevImage,
	"ImagePullBackOff":           sevImage,
	"InvalidImageName":           sevImage,
	"ImageInspectError":          sevImage,
	"RegistryUnavailable":        sevImage,
	"CreateContainerConfigError": sevConfig,
	"CreateContainerError":       sevConfig,
	"ConfigError":                sevConfig,
	"CrashLoopBackOff":           sevCrash,
	"RunContainerError":          sevCrash,
	"StartError":                 sevCrash,
	"PostStartHookError":         sevCrash,
	"ContainerCreating":          sevProgressing,
	"PodInitializing":            sevProgressing,
}

// diagnosePods inspects an environment's pods and returns every problem found.
func diagnosePods(env string, pods []corev1.Pod) []appIssue {
	var issues []appIssue

	for _, pod := range pods {
		// CronJob pods have their own lifecycle and shouldn't colour the app's health.
		if _, isCronPod := pod.Labels["kubernetes.getvesta.sh/cronjob"]; isCronPod {
			continue
		}

		where := fmt.Sprintf("%s/%s", env, pod.Name)

		// Unschedulable pods never produce container statuses, so check conditions.
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason != "" {
				issues = append(issues, appIssue{
					Reason:   cond.Reason,
					Message:  fmt.Sprintf("%s cannot be scheduled: %s", where, firstLine(cond.Message)),
					Severity: sevScheduling,
				})
			}
		}

		statuses := append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...)
		statuses = append(statuses, pod.Status.ContainerStatuses...)

		for _, cs := range statuses {
			target := fmt.Sprintf("%s container %q", where, cs.Name)

			if w := cs.State.Waiting; w != nil && w.Reason != "" {
				severity, known := waitingReasonSeverity[w.Reason]
				if !known {
					severity = sevNotReady
				}
				msg := fmt.Sprintf("%s is not starting (%s)", target, w.Reason)
				if detail := firstLine(w.Message); detail != "" {
					msg = fmt.Sprintf("%s: %s", msg, detail)
				}
				// A crash loop's own message is generic; the previous exit tells the story.
				if w.Reason == "CrashLoopBackOff" {
					if t := cs.LastTerminationState.Terminated; t != nil {
						msg = fmt.Sprintf("%s is crash looping after %d restart(s): %s",
							target, cs.RestartCount, describeTermination(t))
					}
				}
				issues = append(issues, appIssue{Reason: w.Reason, Message: msg, Severity: severity})
				continue
			}

			if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
				issues = append(issues, appIssue{
					Reason:   nonEmpty(t.Reason, "ContainerTerminated"),
					Message:  fmt.Sprintf("%s terminated: %s", target, describeTermination(t)),
					Severity: sevCrash,
				})
				continue
			}

			// Running but never passing its readiness probe is its own failure mode,
			// and one that otherwise just looks like "Degraded" forever.
			if cs.State.Running != nil && !cs.Ready {
				issues = append(issues, appIssue{
					Reason:   "ContainerNotReady",
					Message:  fmt.Sprintf("%s is running but not passing its readiness check", target),
					Severity: sevNotReady,
				})
			}
		}
	}

	return issues
}

// diagnoseDeployment reports Deployment-level conditions that explain a stall,
// such as an exceeded progress deadline or a replica set that cannot create pods.
func diagnoseDeployment(env string, deploy *appsv1.Deployment) []appIssue {
	var issues []appIssue
	for _, cond := range deploy.Status.Conditions {
		switch {
		case cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue:
			issues = append(issues, appIssue{
				Reason:   nonEmpty(cond.Reason, "ReplicaFailure"),
				Message:  fmt.Sprintf("%s cannot create pods: %s", env, firstLine(cond.Message)),
				Severity: sevConfig,
			})
		case cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse:
			issues = append(issues, appIssue{
				Reason:   nonEmpty(cond.Reason, "ProgressDeadlineExceeded"),
				Message:  fmt.Sprintf("%s rollout is stuck: %s", env, firstLine(cond.Message)),
				Severity: sevScheduling,
			})
		}
	}
	return issues
}

// classifyReconcileError turns an operator error into a reason token and message.
// These are the failures that were previously invisible outside the logs.
func classifyReconcileError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "is invalid"), strings.Contains(lower, "duplicate value"),
		strings.Contains(lower, "unsupported value"), strings.Contains(lower, "required value"):
		return "InvalidSpec", msg
	case strings.Contains(lower, "exceeded quota"), strings.Contains(lower, "forbidden: exceeded"):
		return "QuotaExceeded", msg
	case strings.Contains(lower, "forbidden"):
		return "Forbidden", msg
	case strings.Contains(lower, "not found"):
		return "MissingDependency", msg
	case strings.Contains(lower, "already exists"):
		return "Conflict", msg
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "context deadline"):
		return "Timeout", msg
	default:
		return "ReconcileError", msg
	}
}

// worstIssue returns the issue that best explains the app's state: highest
// severity, with a stable tie-break so the message doesn't flap between equally
// severe problems on every reconcile.
func worstIssue(issues []appIssue) *appIssue {
	if len(issues) == 0 {
		return nil
	}
	sorted := append([]appIssue{}, issues...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity > sorted[j].Severity
		}
		if sorted[i].Reason != sorted[j].Reason {
			return sorted[i].Reason < sorted[j].Reason
		}
		return sorted[i].Message < sorted[j].Message
	})
	return &sorted[0]
}

// summarizeIssues builds the status message: the worst issue, plus a count of the
// others so a widespread problem doesn't look like a single pod's.
func summarizeIssues(issues []appIssue) (string, string) {
	worst := worstIssue(issues)
	if worst == nil {
		return "", ""
	}
	message := worst.Message
	if others := countOtherIssues(issues, worst); others > 0 {
		message = fmt.Sprintf("%s (+%d other issue(s))", message, others)
	}
	return worst.Reason, truncateMessage(message)
}

func countOtherIssues(issues []appIssue, worst *appIssue) int {
	n := 0
	for _, i := range issues {
		if i.Message != worst.Message {
			n++
		}
	}
	return n
}

func describeTermination(t *corev1.ContainerStateTerminated) string {
	parts := []string{}
	if t.Reason != "" {
		parts = append(parts, t.Reason)
	}
	// 137 is SIGKILL, which for a container almost always means it hit its memory
	// limit; saying so saves the reader a lookup.
	switch {
	case t.Reason == "OOMKilled":
		parts = append(parts, "container exceeded its memory limit")
	case t.ExitCode == 137:
		parts = append(parts, "killed (exit 137, usually the memory limit)")
	case t.ExitCode != 0:
		parts = append(parts, fmt.Sprintf("exit code %d", t.ExitCode))
	}
	if detail := firstLine(t.Message); detail != "" {
		parts = append(parts, detail)
	}
	if len(parts) == 0 {
		return "terminated for an unknown reason"
	}
	return strings.Join(parts, ", ")
}

// firstLine keeps status messages to one line; Kubernetes messages can be long
// multi-line dumps that make a status field unreadable.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, "\n\r"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

func truncateMessage(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
