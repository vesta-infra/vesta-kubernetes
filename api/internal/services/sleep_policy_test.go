package services

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The safety argument for the whole feature, in one table.
//
// Everything that is not a confident, observed, sustained zero must refuse to sleep. The
// cost of getting that wrong is asymmetric: not sleeping costs a running pod, while sleeping
// wrongly takes a live application down and looks like a platform fault rather than a
// configuration one.
func TestShouldSleepFailsSafe(t *testing.T) {
	now := time.Now()
	longIdle := now.Add(-2 * time.Hour)

	policy := SleepPolicy{AutoSleep: true, InactivityTimeout: 30 * time.Minute, MinAwake: 5 * time.Minute}

	cases := []struct {
		name  string
		probe ActivityProbe
		want  bool
	}{
		// The only case that sleeps.
		{"observed zero over a long idle period", ActivityProbe{Observed: true, Requests: 0}, true},

		// Could not ask. Prometheus down, unreachable, or rejecting the query.
		{"query failed", ActivityProbe{Err: errors.New("connection refused")}, false},

		// Asked, and nothing came back. This is the one that matters most: an empty result
		// is evidence of no metric, not evidence of no traffic. Treating the two as the
		// same is how a busy app gets scaled to zero.
		{"no series for this app", ActivityProbe{Observed: false}, false},
		{"no series, and a zero alongside it", ActivityProbe{Observed: false, Requests: 0}, false},

		// Traffic.
		{"requests in the window", ActivityProbe{Observed: true, Requests: 42}, false},
		{"a single request", ActivityProbe{Observed: true, Requests: 1}, false},
		{"a fractional request", ActivityProbe{Observed: true, Requests: 0.4}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldSleep(policy, tc.probe, now, longIdle, time.Time{}, 1)
			if got.Sleep != tc.want {
				t.Errorf("ShouldSleep(%+v) = %v (%q), want %v", tc.probe, got.Sleep, got.Reason, tc.want)
			}
			if !got.Sleep && got.Reason == "" {
				t.Error("a refusal carried no reason; it is shown next to the auto-sleep toggle, " +
					"and without it the feature looks broken rather than waiting")
			}
		})
	}
}

// Opting in is required. Eligibility alone must never sleep anything, because every app
// upgraded from a release where scale-to-zero did nothing carries sleep.enabled=true.
func TestAutoSleepMustBeOn(t *testing.T) {
	now := time.Now()
	idle := ActivityProbe{Observed: true, Requests: 0}

	off := ShouldSleep(SleepPolicy{AutoSleep: false}, idle, now, now.Add(-2*time.Hour), time.Time{}, 1)
	if off.Sleep {
		t.Error("an app that never opted into auto-sleep was put to sleep")
	}
	if !strings.Contains(off.Reason, "off") {
		t.Errorf("reason = %q, want it to say auto-sleep is off", off.Reason)
	}
}

// An app that is already down must not be "slept" again, which would be a pointless write on
// every sweep.
func TestAlreadyAsleepIsNotSleptAgain(t *testing.T) {
	now := time.Now()
	d := ShouldSleep(
		SleepPolicy{AutoSleep: true},
		ActivityProbe{Observed: true, Requests: 0},
		now, now.Add(-2*time.Hour), time.Time{}, 0,
	)
	if d.Sleep {
		t.Error("an app with no ready replicas was slept again")
	}
}

func TestInactivityWindowIsRespected(t *testing.T) {
	now := time.Now()
	policy := SleepPolicy{AutoSleep: true, InactivityTimeout: 30 * time.Minute}
	idle := ActivityProbe{Observed: true, Requests: 0}

	if d := ShouldSleep(policy, idle, now, now.Add(-29*time.Minute), time.Time{}, 1); d.Sleep {
		t.Errorf("slept after 29 minutes with a 30 minute timeout: %q", d.Reason)
	}
	if d := ShouldSleep(policy, idle, now, now.Add(-31*time.Minute), time.Time{}, 1); !d.Sleep {
		t.Errorf("did not sleep after 31 minutes with a 30 minute timeout: %q", d.Reason)
	}

	// An app that names no timeout gets a sensible one rather than sleeping instantly.
	none := SleepPolicy{AutoSleep: true}
	if d := ShouldSleep(none, idle, now, now.Add(-time.Minute), time.Time{}, 1); d.Sleep {
		t.Error("an app with no configured timeout slept after one minute")
	}
}

// Without a floor after waking, the request that woke the app is often the only traffic in
// the window -- so it sleeps again immediately, and the next request wakes it again. The app
// flaps and every request pays the wake latency.
func TestMinAwakePreventsFlapping(t *testing.T) {
	now := time.Now()
	policy := SleepPolicy{AutoSleep: true, InactivityTimeout: time.Minute, MinAwake: 10 * time.Minute}
	idle := ActivityProbe{Observed: true, Requests: 0}

	justWoken := ShouldSleep(policy, idle, now, now.Add(-5*time.Minute), now.Add(-2*time.Minute), 1)
	if justWoken.Sleep {
		t.Errorf("slept two minutes after waking with a ten minute floor: %q", justWoken.Reason)
	}

	settled := ShouldSleep(policy, idle, now, now.Add(-30*time.Minute), now.Add(-30*time.Minute), 1)
	if !settled.Sleep {
		t.Errorf("did not sleep well past the floor: %q", settled.Reason)
	}

	// An app that has never been woken has no floor to respect.
	never := ShouldSleep(policy, idle, now, now.Add(-30*time.Minute), time.Time{}, 1)
	if !never.Sleep {
		t.Errorf("an app that was never woken was held up by the floor: %q", never.Reason)
	}
}

// The query has to work whichever ingress controller is installed, and whichever way its
// metrics happened to be scraped. A query that matches nothing is indistinguishable from an
// idle app, which is exactly the confusion the three-valued probe exists to prevent -- so it
// is worth asking every way at once.
func TestRequestRateQueryCoversBothControllers(t *testing.T) {
	q := RequestRateQuery("acme-production", "web", 30*time.Minute)

	for _, want := range []string{
		"traefik_service_requests_total",
		"nginx_ingress_controller_requests",
		// Both label spellings: which one exists depends on how Prometheus was told to
		// scrape the controller.
		"exported_service=~",
		"service=~",
		"exported_namespace=",
		"namespace=",
		"acme-production",
		"web",
		"[30m]",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query does not contain %q:\n%s", want, q)
		}
	}

	// increase(), not rate(): the question is whether there were any requests at all, and a
	// rate that rounds to zero over a long window is not the same as none.
	if strings.Contains(q, "rate(") && !strings.Contains(q, "increase(") {
		t.Error("the query uses rate(); a low rate rounds to zero and would sleep a live app")
	}
}

func TestPromDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Minute: "30m",
		time.Hour:        "1h",
		2 * time.Hour:    "2h",
		90 * time.Minute: "90m",
		time.Second:      "1m", // never below a minute, or the window is meaningless
		0:                "1m",
	}
	for d, want := range cases {
		if got := promDuration(d); got != want {
			t.Errorf("promDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
