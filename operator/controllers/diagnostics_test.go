package controllers

import (
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podWithContainer(name string, cs corev1.ContainerStatus) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

func TestDiagnosePodsImagePullNamesTheImage(t *testing.T) {
	pod := podWithContainer("web-1", corev1.ContainerStatus{
		Name: "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "ImagePullBackOff",
			Message: `Back-off pulling image "registry.internal/gateway:v12"`,
		}},
	})

	reason, message := summarizeIssues(diagnosePods("production", []corev1.Pod{pod}))
	if reason != "ImagePullBackOff" {
		t.Fatalf("reason = %q", reason)
	}
	for _, want := range []string{"production/web-1", `"app"`, "registry.internal/gateway:v12"} {
		if !strings.Contains(message, want) {
			t.Errorf("message missing %q: %s", want, message)
		}
	}
}

func TestDiagnosePodsCrashLoopUsesPreviousExit(t *testing.T) {
	pod := podWithContainer("api-7", corev1.ContainerStatus{
		Name:         "app",
		RestartCount: 5,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "CrashLoopBackOff",
			Message: "back-off 5m0s restarting failed container",
		}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:   "Error",
			ExitCode: 1,
		}},
	})

	reason, message := summarizeIssues(diagnosePods("staging", []corev1.Pod{pod}))
	if reason != "CrashLoopBackOff" {
		t.Fatalf("reason = %q", reason)
	}
	// The generic back-off text explains nothing; the exit code does.
	if !strings.Contains(message, "exit code 1") || !strings.Contains(message, "5 restart(s)") {
		t.Errorf("message should describe the previous exit: %s", message)
	}
}

func TestDiagnosePodsOOMKilledIsExplained(t *testing.T) {
	pod := podWithContainer("worker-2", corev1.ContainerStatus{
		Name:  "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason:   "OOMKilled",
			ExitCode: 137,
		}},
	})

	_, message := summarizeIssues(diagnosePods("production", []corev1.Pod{pod}))
	if !strings.Contains(message, "memory limit") {
		t.Errorf("an OOM kill should say so in words: %s", message)
	}
}

func TestDiagnosePodsUnschedulable(t *testing.T) {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1"},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  "Unschedulable",
				Message: "0/3 nodes are available: 3 Insufficient cpu.",
			}},
		},
	}

	reason, message := summarizeIssues(diagnosePods("production", []corev1.Pod{pod}))
	if reason != "Unschedulable" {
		t.Fatalf("reason = %q", reason)
	}
	if !strings.Contains(message, "Insufficient cpu") {
		t.Errorf("message should carry the scheduler's explanation: %s", message)
	}
}

func TestDiagnosePodsConfigErrorOutranksNotReady(t *testing.T) {
	// A missing secret is the cause; another pod merely not being ready is a symptom.
	configErr := podWithContainer("web-1", corev1.ContainerStatus{
		Name: "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "CreateContainerConfigError",
			Message: `secret "stripe-live" not found`,
		}},
	})
	notReady := podWithContainer("web-2", corev1.ContainerStatus{
		Name:  "app",
		Ready: false,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})

	reason, message := summarizeIssues(diagnosePods("production", []corev1.Pod{configErr, notReady}))
	if reason != "CreateContainerConfigError" {
		t.Fatalf("the config error should win, got %q", reason)
	}
	if !strings.Contains(message, "stripe-live") {
		t.Errorf("message should name the missing secret: %s", message)
	}
	// The other problem is still counted so a widespread issue isn't hidden.
	if !strings.Contains(message, "+1 other") {
		t.Errorf("message should note the other issue: %s", message)
	}
}

func TestDiagnosePodsIgnoresCronJobPods(t *testing.T) {
	pod := podWithContainer("nightly-abc", corev1.ContainerStatus{
		Name:  "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	})
	pod.Labels = map[string]string{"kubernetes.getvesta.sh/cronjob": "nightly"}

	if issues := diagnosePods("production", []corev1.Pod{pod}); len(issues) != 0 {
		t.Fatalf("cron pods have their own lifecycle, got %+v", issues)
	}
}

func TestDiagnosePodsHealthyPodHasNoIssues(t *testing.T) {
	pod := podWithContainer("web-1", corev1.ContainerStatus{
		Name:  "app",
		Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	if issues := diagnosePods("production", []corev1.Pod{pod}); len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
	reason, message := summarizeIssues(nil)
	if reason != "" || message != "" {
		t.Errorf("a healthy app should carry no reason, got %q/%q", reason, message)
	}
}

func TestDiagnoseDeploymentConditions(t *testing.T) {
	deploy := &appsv1.Deployment{Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{
		{
			Type:    appsv1.DeploymentReplicaFailure,
			Status:  corev1.ConditionTrue,
			Reason:  "FailedCreate",
			Message: `pods "web-" is forbidden: exceeded quota: cpu`,
		},
	}}}

	reason, message := summarizeIssues(diagnoseDeployment("production", deploy))
	if reason != "FailedCreate" {
		t.Fatalf("reason = %q", reason)
	}
	if !strings.Contains(message, "exceeded quota") {
		t.Errorf("message should carry the quota error: %s", message)
	}
}

func TestClassifyReconcileError(t *testing.T) {
	cases := []struct {
		err        string
		wantReason string
	}{
		// The duplicate port failure that was previously invisible outside the logs.
		{`Deployment.apps "gateway" is invalid: spec.template.spec.containers[0].ports[1].name: Duplicate value: "http"`, "InvalidSpec"},
		{`pods "web" is forbidden: exceeded quota: compute-resources`, "QuotaExceeded"},
		{`secrets "db" is forbidden: User cannot get resource`, "Forbidden"},
		{`secret "db-creds" not found`, "MissingDependency"},
		{`service "gateway" already exists`, "Conflict"},
		{`context deadline exceeded`, "Timeout"},
		{`something unexpected happened`, "ReconcileError"},
	}
	for _, c := range cases {
		reason, message := classifyReconcileError(fmt.Errorf("%s", c.err))
		if reason != c.wantReason {
			t.Errorf("classify(%q) = %q, want %q", c.err, reason, c.wantReason)
		}
		if message != c.err {
			t.Errorf("message should be preserved verbatim, got %q", message)
		}
	}
	if reason, _ := classifyReconcileError(nil); reason != "" {
		t.Errorf("nil error should classify to empty, got %q", reason)
	}
}

func TestMessagesStayOneLineAndBounded(t *testing.T) {
	pod := podWithContainer("web-1", corev1.ContainerStatus{
		Name: "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason:  "CreateContainerConfigError",
			Message: "first line\nsecond line\nthird line",
		}},
	})
	_, message := summarizeIssues(diagnosePods("production", []corev1.Pod{pod}))
	if strings.ContainsAny(message, "\n\r") {
		t.Errorf("status messages must stay on one line: %q", message)
	}
	if strings.Contains(message, "second line") {
		t.Errorf("only the first line should be kept: %q", message)
	}

	long := truncateMessage(strings.Repeat("x", 900))
	if len(long) > 512 {
		t.Errorf("message should be truncated, got %d chars", len(long))
	}
	if !strings.HasSuffix(long, "...") {
		t.Errorf("truncation should be visible: %q", long[len(long)-10:])
	}
}
