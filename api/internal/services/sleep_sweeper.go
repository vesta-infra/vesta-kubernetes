package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
)

// SleepSweeper scales idle apps to zero.
//
// It lives in the API rather than the operator for three reasons, and the third is the one
// that decided it.
//
// The Prometheus client, its service discovery and the request-rate query already exist
// here; putting the sweeper in the operator would mean a second copy of all of it. Its only
// output is a spec patch, which the API already does everywhere. And the failure asymmetry
// is right this way round: if the API is down, nothing sleeps -- apps stay up, which is the
// harmless direction -- and it can never prevent a wake, because the activator talks to the
// Kubernetes API directly and does not know this exists.
type SleepSweeper struct {
	DB  *db.DB
	K8s *k8s.Client

	// lastActive and lastWoke track what Prometheus cannot: when an app was last seen busy,
	// and when it last came up. Held in memory because losing them on restart is harmless --
	// an app simply waits one more inactivity window before it can sleep.
	mu         sync.Mutex
	lastActive map[string]time.Time
	lastWoke   map[string]time.Time
	// lastReady is the previous observation, so a 0-to-many transition can be recognised
	// as a wake.
	lastReady map[string]int32

	// promURL is discovered once and remembered. Rediscovering per sweep would probe five
	// well-known service locations every minute for an answer that almost never changes.
	promURL     string
	promChecked time.Time
}

// sweepInterval is how often idleness is reassessed. Frequent enough that an app sleeps
// reasonably soon after going quiet, rare enough that the query load is negligible.
const sweepInterval = 2 * time.Minute

// promRediscoverAfter bounds how long a failed discovery is remembered, so a Prometheus
// installed after Vesta is eventually found without a restart.
const promRediscoverAfter = 10 * time.Minute

func NewSleepSweeper(database *db.DB, kc *k8s.Client) *SleepSweeper {
	return &SleepSweeper{
		DB: database, K8s: kc,
		lastActive: map[string]time.Time{},
		lastWoke:   map[string]time.Time{},
		lastReady:  map[string]int32{},
	}
}

func (s *SleepSweeper) Start(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	log.Println("[sleep] inactivity sweeper started")
	for {
		select {
		case <-ctx.Done():
			log.Println("[sleep] sweeper stopped")
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *SleepSweeper) sweep(ctx context.Context) {
	// One replica sweeps at a time. Two API replicas both deciding to sleep the same app
	// is harmless, but both querying Prometheus for every app is not, and the lock is
	// nearly free.
	locked, release, err := s.DB.TryAdvisoryLock(ctx, db.AdvisoryLockSleepSweeper)
	if err != nil || !locked {
		return
	}
	defer release()

	apps, err := s.K8s.ListResources(ctx, k8s.VestaAppGVR, vestaSystemNS, "")
	if err != nil {
		log.Printf("[sleep] listing apps: %v", err)
		return
	}

	promURL := s.prometheusURL(ctx)

	for i := range apps.Items {
		app := apps.Items[i]
		s.considerApp(ctx, app.Object, app.GetName(), promURL)
	}
}

func (s *SleepSweeper) considerApp(ctx context.Context, obj map[string]interface{}, name, promURL string) {
	spec, _ := obj["spec"].(map[string]interface{})
	sleepSpec, _ := spec["sleep"].(map[string]interface{})
	if sleepSpec == nil {
		return
	}

	policy := policyFrom(sleepSpec)
	if !policy.AutoSleep {
		return
	}

	project, _ := spec["project"].(string)
	status, _ := obj["status"].(map[string]interface{})

	// An app already at rest has nothing to do. Checked from the spec, which is the
	// instruction, rather than from the phase, which is an observation that lags it.
	if desired, _ := spec["desiredState"].(string); desired == "sleeping" || desired == "stopped" {
		return
	}

	ready := readyReplicasFromStatus(status)

	// Notice a wake by watching for the transition, rather than only being told about one.
	//
	// The activator wakes apps by patching Kubernetes directly and never touches this
	// process, so a notification-only approach would miss exactly the wakes that matter --
	// and the sweeper could put an app back to sleep in the gap before its first request
	// shows up in Prometheus.
	s.mu.Lock()
	wasDown := s.lastReady[name] == 0
	if ready > 0 && wasDown {
		s.lastWoke[name] = time.Now()
	}
	s.lastReady[name] = ready
	s.mu.Unlock()

	for _, namespace := range namespacesFor(project, spec) {
		probe := s.probe(ctx, promURL, namespace, name, policy.InactivityTimeout)

		key := namespace + "/" + name
		s.mu.Lock()
		if probe.Observed && probe.Requests > 0 {
			s.lastActive[key] = time.Now()
		}
		lastActive, seen := s.lastActive[key]
		if !seen {
			// First sight of this app. Start the clock now rather than treating "we have
			// never looked" as "idle since the beginning of time", which would sleep every
			// eligible app on the first sweep after a restart.
			lastActive = time.Now()
			s.lastActive[key] = lastActive
		}
		lastWoke := s.lastWoke[name]
		s.mu.Unlock()

		decision := ShouldSleep(policy, probe, time.Now(), lastActive, lastWoke, ready)
		s.recordReason(ctx, name, decision)

		if !decision.Sleep {
			continue
		}

		if err := s.sleepApp(ctx, name); err != nil {
			log.Printf("[sleep] could not sleep %s: %v", name, err)
			continue
		}
		log.Printf("[sleep] scaled %s to zero: %s", name, decision.Reason)
	}
}

// probe asks Prometheus whether an app saw any requests.
//
// Every failure produces Observed=false rather than a zero, which is the whole safety
// property: the caller can then tell "no traffic" from "no answer", and only the first of
// those sleeps anything.
func (s *SleepSweeper) probe(ctx context.Context, promURL, namespace, app string, window time.Duration) ActivityProbe {
	if promURL == "" {
		return ActivityProbe{Err: fmt.Errorf("no Prometheus found")}
	}
	if window <= 0 {
		window = DefaultInactivityTimeout
	}

	query := RequestRateQuery(namespace, app, window)
	end := time.Now()

	result, err := s.K8s.QueryPrometheusRange(ctx, promURL, query, end.Add(-window), end, window)
	if err != nil {
		return ActivityProbe{Err: err}
	}
	if result == nil || len(result.Results) == 0 {
		// No series. Not zero traffic -- no metric.
		return ActivityProbe{Observed: false}
	}

	total := 0.0
	for _, series := range result.Results {
		for _, point := range series.Values {
			var f float64
			if _, err := fmt.Sscanf(point.Value, "%g", &f); err == nil {
				total += f
			}
		}
	}
	return ActivityProbe{Observed: true, Requests: total}
}

// sleepApp asks the operator to scale the app to zero.
//
// A spec patch, not a status one: the instruction lives in spec.desiredState, and a patch to
// status is discarded by the subresource -- which is exactly how scale-to-zero came to do
// nothing at all for as long as it did.
func (s *SleepSweeper) sleepApp(ctx context.Context, name string) error {
	patch := []byte(`{"spec":{"desiredState":"sleeping"}}`)
	_, err := s.K8s.PatchResource(ctx, k8s.VestaAppGVR, vestaSystemNS, name, patch)
	return err
}

// recordReason publishes why an app is or is not sleeping.
//
// Without this the feature is invisible when it is doing nothing: an admin who turned on
// auto-sleep and sees an app still running has no way to tell whether it is busy, whether
// Prometheus is missing, or whether Vesta is simply not looking.
func (s *SleepSweeper) recordReason(ctx context.Context, name string, d SleepDecision) {
	patch, err := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{"sleepReason": d.Reason},
	})
	if err != nil {
		return
	}
	// Through the status subresource, since this is an observation.
	if _, err := s.K8s.PatchResourceStatus(ctx, k8s.VestaAppGVR, vestaSystemNS, name, patch); err != nil {
		// Reporting is not worth failing a sweep over.
		return
	}
}

// NoteWoken records that an app has just been started, so the sweeper leaves it up for at
// least MinAwake.
//
// Called by the API's own wake path. It is not the only way a wake is noticed -- the sweeper
// also watches for a replica count going from zero, which is what catches the activator
// waking an app without this process being involved -- but it is the immediate one, and it
// closes the window before the next sweep observes the transition.
func (s *SleepSweeper) NoteWoken(app string) {
	s.mu.Lock()
	s.lastWoke[app] = time.Now()
	s.mu.Unlock()
}

func (s *SleepSweeper) prometheusURL(ctx context.Context) string {
	s.mu.Lock()
	cached, checked := s.promURL, s.promChecked
	s.mu.Unlock()

	if cached != "" || time.Since(checked) < promRediscoverAfter {
		return cached
	}

	found := s.K8s.DiscoverPrometheusURL(ctx)

	s.mu.Lock()
	s.promURL, s.promChecked = found, time.Now()
	s.mu.Unlock()

	if found == "" {
		log.Printf("[sleep] no Prometheus found; nothing will be slept automatically")
	}
	return found
}

func policyFrom(sleepSpec map[string]interface{}) SleepPolicy {
	auto, _ := sleepSpec["autoSleep"].(bool)
	enabled, _ := sleepSpec["enabled"].(bool)

	return SleepPolicy{
		// Both are required. Eligibility alone must never sleep anything, because every app
		// upgraded from a release where this did nothing carries enabled=true.
		AutoSleep:         enabled && auto,
		InactivityTimeout: parseDurationOr(sleepSpec["inactivityTimeout"], DefaultInactivityTimeout),
		MinAwake:          parseDurationOr(sleepSpec["minAwake"], DefaultMinAwake),
	}
}

func parseDurationOr(v interface{}, fallback time.Duration) time.Duration {
	s, _ := v.(string)
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func readyReplicasFromStatus(status map[string]interface{}) int32 {
	scaling, _ := status["scaling"].(map[string]interface{})
	switch v := scaling["currentReplicas"].(type) {
	case int64:
		return int32(v)
	case float64:
		return int32(v)
	}
	return 0
}

// namespacesFor lists the namespaces an app runs in.
func namespacesFor(project string, spec map[string]interface{}) []string {
	envs, _ := spec["environments"].([]interface{})
	if len(envs) == 0 {
		return nil
	}

	out := make([]string, 0, len(envs))
	for _, e := range envs {
		m, _ := e.(map[string]interface{})
		if name, _ := m["name"].(string); name != "" {
			out = append(out, project+"-"+name)
		}
	}
	return out
}
