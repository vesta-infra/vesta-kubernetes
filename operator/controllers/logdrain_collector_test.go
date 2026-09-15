package controllers

import (
	"os"
	"strings"
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// A drain whose config renders fine but has no collector to run it reported Ready.
//
// logging.enabled defaults to false in the chart, so no Fluent Bit DaemonSet exists on a
// default install. Creating a drain wrote the ConfigMap, resolveTargets accepted it, and
// status.Ready came back true — while not one line was being shipped. The only trace was a
// V(1) log nobody sees.
//
// A drain that claims to work and does not is worse than one that says it cannot: the first
// sign of trouble is an empty dashboard during an incident.
func TestDrainIsNotReadyWithoutACollector(t *testing.T) {
	raw, err := os.ReadFile("vestalogdrain_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	if !strings.Contains(src, "case collectorMissing:") {
		t.Fatal("updateStatus does not branch on a missing collector, so a drain with nowhere " +
			"to ship still reports Ready")
	}

	// The branch must come before the default that sets Ready = true.
	missing := strings.Index(src, "case collectorMissing:")
	ready := strings.Index(src, "status.Ready = true")
	if missing < 0 || ready < 0 || missing > ready {
		t.Error("the collector-missing branch does not precede the one that sets Ready")
	}

	// And it has to say what to do about it, since the fix is a chart value.
	branch := src[missing:]
	if end := strings.Index(branch, "\n\tcase "); end > 0 {
		branch = branch[:end]
	}
	if !strings.Contains(branch, "logging.enabled") {
		t.Error("the reason does not name the chart value that turns the collector on")
	}
}

// The status a drain reports has to be the one the UI can act on, so the reason is a
// sentence rather than a code.
func TestCollectorMissingReasonIsReadable(t *testing.T) {
	var status vestav1alpha1.VestaLogDrainStatus
	status.Reason = "no log collector is running — set logging.enabled=true in the chart"
	if status.Ready {
		t.Error("a status carrying this reason must not also be ready")
	}
	if len(strings.Fields(status.Reason)) < 5 {
		t.Error("the reason is too terse to act on")
	}
}
