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

// A drain answering 401 on every flush reported {"ready": true} with no counters at all.
//
// collectorStats returns nil for every failure -- unreachable collector, timeout, unparseable
// body -- and nil was indistinguishable from "read fine, this drain delivered nothing". So
// updateStatus set Ready and skipped the error block, and the status said healthy while not
// one record was landing.
func TestUnreadableStatsAreNotReportedAsHealth(t *testing.T) {
	raw, err := os.ReadFile("vestalogdrain_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	// The scrape has to say whether it succeeded, or the caller cannot tell the two apart.
	if !strings.Contains(src, "(map[string]outputStats, bool)") {
		t.Error("collectorStats does not report whether it read anything; a failed scrape is " +
			"indistinguishable from a drain that delivered nothing")
	}
	if !strings.Contains(src, "if !statsRead {") {
		t.Error("updateStatus does not act on an unreadable scrape, so it still claims health " +
			"on no evidence")
	}

	// And it has to say so rather than silently carrying on.
	i := strings.Index(src, "if !statsRead {")
	if i < 0 {
		return
	}
	branch := src[i : i+300]
	if !strings.Contains(branch, "status.Reason") {
		t.Error("the unreadable case sets no reason, so nothing reaches the drain list")
	}
}

// Confirmed errors must still mark the drain unhealthy — that path is what the reason above
// must not displace.
func TestDeliveryErrorsStillFailTheDrain(t *testing.T) {
	raw, _ := os.ReadFile("vestalogdrain_controller.go")
	src := string(raw)
	if !strings.Contains(src, "status.Ready = false") {
		t.Error("no path sets Ready false from delivery errors")
	}
	if !strings.Contains(src, "delivery errors") {
		t.Error("the delivery-error reason is gone")
	}
}
