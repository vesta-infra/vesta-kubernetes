package controllers

import (
	"strings"
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Scale-to-zero does not survive contact with monitoring unless health checks are excluded
// by default.
//
// An uptime check polling the public URL every thirty seconds wakes the app every thirty
// seconds. It never stays down, the feature saves nothing, and the cause is invisible --
// from the outside the app just looks busy. The mechanism for this existed already; what was
// missing is that it did nothing until somebody configured it by hand.
func TestHealthPathsAreExcludedWithoutConfiguration(t *testing.T) {
	sleep := &vestav1alpha1.SleepConfig{Enabled: true}

	list := sleep.NoWakePathList()
	if list == "" {
		t.Fatal("an app that configured no paths gets no exclusions, so any health check " +
			"against the public URL wakes it and it never stays asleep")
	}

	for _, want := range []string{"/healthz", "/health", "/readyz"} {
		if !strings.Contains(list, want) {
			t.Errorf("the default list does not cover %q: %s", want, list)
		}
	}

	// Paths that are real application endpoints often enough that answering them from the
	// activator would be worse than the problem being solved.
	for _, unwanted := range []string{"/status", "/ping", "/"} {
		for _, got := range strings.Split(list, ",") {
			if got == unwanted {
				t.Errorf("%q is excluded by default; a request the app should have served "+
					"would get a stub from the activator instead", unwanted)
			}
		}
	}
}

// Configuring a list replaces the defaults rather than adding to them, so an app that needs
// a different set is not stuck carrying these as well.
func TestConfiguredPathsReplaceTheDefaults(t *testing.T) {
	sleep := &vestav1alpha1.SleepConfig{Enabled: true, NoWakePaths: []string{"/_internal/alive"}}

	got := sleep.NoWakePathList()
	if got != "/_internal/alive" {
		t.Errorf("= %q, want exactly the configured path", got)
	}
	if strings.Contains(got, "/healthz") {
		t.Error("the defaults were merged in; a configured list must replace them")
	}
}

// A nil config has nothing to say, and must not stamp an annotation.
func TestNoSleepConfigProducesNoPaths(t *testing.T) {
	var sleep *vestav1alpha1.SleepConfig
	if got := sleep.NoWakePathList(); got != "" {
		t.Errorf("= %q, want empty for an app with no sleep config", got)
	}
}
