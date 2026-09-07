package handlers

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pod(name string, statuses ...corev1.ContainerStatus) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.PodStatus{ContainerStatuses: statuses},
	}
}

func TestDiagnosePodForAPIImagePull(t *testing.T) {
	issues := diagnosePodForAPI(pod("gateway-abc", corev1.ContainerStatus{
		Name: "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "ImagePullBackOff",
			Message: `Back-off pulling image "registry.internal/gateway:v12"`,
		}},
	}))

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %+v", issues)
	}
	got := issues[0]
	if got.Reason != "ImagePullBackOff" || got.Pod != "gateway-abc" || got.Container != "app" {
		t.Errorf("issue does not identify the failure: %+v", got)
	}
	// The hint is the part that makes the reason actionable for a non-cluster user.
	if !strings.Contains(got.Hint, "registry credential") {
		t.Errorf("expected an actionable hint, got %q", got.Hint)
	}
}

func TestDiagnosePodForAPICrashLoopReportsExit(t *testing.T) {
	issues := diagnosePodForAPI(pod("api-1", corev1.ContainerStatus{
		Name:         "app",
		RestartCount: 7,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "CrashLoopBackOff",
			Message: "back-off 5m0s restarting failed container",
		}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:   "Error",
			ExitCode: 127,
		}},
	}))

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "exit code 127") {
		t.Errorf("the previous exit should replace the generic back-off text: %q", issues[0].Message)
	}
	if issues[0].Restarts != 7 {
		t.Errorf("restart count lost: %+v", issues[0])
	}
}

func TestDiagnosePodForAPIOOMGetsMemoryHint(t *testing.T) {
	issues := diagnosePodForAPI(pod("worker-1", corev1.ContainerStatus{
		Name:  "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:   "OOMKilled",
			ExitCode: 137,
		}},
	}))
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %+v", issues)
	}
	// An OOM kill needs the memory hint, not the crash-loop one.
	if !strings.Contains(issues[0].Hint, "memory limit") {
		t.Errorf("expected the OOM hint, got %q", issues[0].Hint)
	}
}

func TestDiagnosePodForAPIIgnoresNormalStartup(t *testing.T) {
	for _, reason := range []string{"ContainerCreating", "PodInitializing"} {
		issues := diagnosePodForAPI(pod("web-1", corev1.ContainerStatus{
			Name:  "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
		}))
		if len(issues) != 0 {
			t.Errorf("%s is normal startup, not a problem: %+v", reason, issues)
		}
	}
}

func TestDiagnosePodForAPIUnreadyContainerIsWarning(t *testing.T) {
	issues := diagnosePodForAPI(pod("web-1", corev1.ContainerStatus{
		Name:  "app",
		Ready: false,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}))
	if len(issues) != 1 || issues[0].Severity != "warning" || issues[0].Reason != "ContainerNotReady" {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if !strings.Contains(issues[0].Hint, "health check") {
		t.Errorf("expected a readiness hint, got %q", issues[0].Hint)
	}
}

func TestDiagnosePodForAPIHealthyPodIsQuiet(t *testing.T) {
	issues := diagnosePodForAPI(pod("web-1", corev1.ContainerStatus{
		Name:  "app",
		Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}))
	if len(issues) != 0 {
		t.Fatalf("a healthy pod should report nothing, got %+v", issues)
	}
}

func TestDiagnosePodForAPIUnschedulable(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1"},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
			Type:    corev1.PodScheduled,
			Status:  corev1.ConditionFalse,
			Reason:  "Unschedulable",
			Message: "0/3 nodes are available: 3 Insufficient memory.",
		}}},
	}
	issues := diagnosePodForAPI(p)
	if len(issues) != 1 || issues[0].Reason != "Unschedulable" {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "Insufficient memory") {
		t.Errorf("scheduler explanation lost: %q", issues[0].Message)
	}
}

func TestRelevantEventsFiltersAndCaps(t *testing.T) {
	events := []corev1.Event{
		{Type: corev1.EventTypeWarning, Reason: "Failed", Message: "pull failed", InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "gateway-abc"}, Count: 4},
		{Type: corev1.EventTypeNormal, Reason: "Pulled", Message: "pulled image", InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "gateway-abc"}},
		{Type: corev1.EventTypeWarning, Reason: "Failed", Message: "other app", InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "unrelated-xyz"}},
	}

	got := relevantEvents(events, "gateway", map[string]bool{"gateway-abc": true})
	if len(got) != 1 {
		t.Fatalf("expected only this app's warning, got %+v", got)
	}
	if got[0].Object != "pod/gateway-abc" || got[0].Count != 4 {
		t.Errorf("unexpected event: %+v", got[0])
	}
}

func TestFirstLineOfTrimsAndBounds(t *testing.T) {
	if got := firstLineOf("line one\nline two"); got != "line one" {
		t.Errorf("got %q", got)
	}
	long := firstLineOf(strings.Repeat("y", 500))
	if len(long) > 300 {
		t.Errorf("expected truncation, got %d chars", len(long))
	}
}

func TestEveryFailureReasonHasAHint(t *testing.T) {
	// A reason without a hint is the exact gap this feature exists to close.
	for _, reason := range []string{
		"ImagePullBackOff", "CrashLoopBackOff", "OOMKilled", "Unschedulable",
		"CreateContainerConfigError", "InvalidSpec", "QuotaExceeded", "ContainerNotReady",
	} {
		if hintFor(reason) == "" {
			t.Errorf("%s has no hint", reason)
		}
	}
}
