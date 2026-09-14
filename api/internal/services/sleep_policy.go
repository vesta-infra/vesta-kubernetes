package services

import (
	"fmt"
	"time"
)

// Deciding when an app is idle enough to scale to zero.
//
// The whole risk in this feature lives in one place: mistaking "I could not find out" for
// "there was no traffic". Prometheus may be absent, the query may fail, the ingress
// controller may export metrics under a name this does not know, or the series may not exist
// yet for a newly created app. Every one of those looks identical to a quiet app if you only
// check whether the result is zero -- and getting it wrong takes a live application down.
//
// So the answer is three-valued, not two, and only an explicit observed zero sleeps anything.

// ActivityProbe is what was learned about an app's traffic.
type ActivityProbe struct {
	// Observed is true only when a metric series for this app was actually found. An empty
	// result is not evidence of no traffic; it is evidence of no metric.
	Observed bool
	// Requests is the number of requests over the window, when Observed.
	Requests float64
	// Err is set when the query could not be answered at all.
	Err error
}

// SleepPolicy is an app's configured intent.
type SleepPolicy struct {
	AutoSleep         bool
	InactivityTimeout time.Duration
	MinAwake          time.Duration
}

// SleepDecision is the outcome, with the reason it was reached.
//
// The reason is not decoration: it is surfaced in the UI next to the auto-sleep toggle, so a
// feature that is quietly doing nothing says why rather than looking broken.
type SleepDecision struct {
	Sleep  bool
	Reason string
}

// Default bounds, used when an app names none.
const (
	DefaultInactivityTimeout = 30 * time.Minute
	// DefaultMinAwake stops an app flapping. The request that woke it is often the only
	// traffic in the window, so without a floor the app sleeps again immediately and the
	// next request wakes it again.
	DefaultMinAwake = 5 * time.Minute
)

// ShouldSleep decides whether an app may be scaled to zero.
//
// Every branch that is not a confident, observed, sustained zero returns false. That is the
// design: the cost of not sleeping is a running pod, and the cost of sleeping wrongly is an
// outage that looks like a platform fault.
func ShouldSleep(p SleepPolicy, probe ActivityProbe, now, lastActive, lastWoke time.Time, readyReplicas int32) SleepDecision {
	if !p.AutoSleep {
		return SleepDecision{false, "auto-sleep is off for this app"}
	}
	if readyReplicas == 0 {
		return SleepDecision{false, "already scaled to zero"}
	}

	// Could not ask.
	if probe.Err != nil {
		return SleepDecision{false, "activity signal unavailable: " + probe.Err.Error()}
	}
	// Asked, and there is nothing to read. Usually an ingress controller whose metrics are
	// not scraped, or an app that has never received a request and so has no series yet.
	if !probe.Observed {
		return SleepDecision{false, "no activity metric for this app, so idleness cannot be established"}
	}
	if probe.Requests > 0 {
		return SleepDecision{false, fmt.Sprintf("%.0f requests in the last window", probe.Requests)}
	}

	timeout := p.InactivityTimeout
	if timeout <= 0 {
		timeout = DefaultInactivityTimeout
	}
	if idle := now.Sub(lastActive); idle < timeout {
		return SleepDecision{false, fmt.Sprintf("idle for %s, needs %s", round(idle), round(timeout))}
	}

	minAwake := p.MinAwake
	if minAwake <= 0 {
		minAwake = DefaultMinAwake
	}
	if !lastWoke.IsZero() {
		if awake := now.Sub(lastWoke); awake < minAwake {
			return SleepDecision{false, fmt.Sprintf("woken %s ago, staying up for at least %s",
				round(awake), round(minAwake))}
		}
	}

	return SleepDecision{true, fmt.Sprintf("no requests for %s", round(now.Sub(lastActive)))}
}

func round(d time.Duration) time.Duration { return d.Round(time.Minute) }

// RequestRateQuery builds the PromQL that counts requests to an app over a window.
//
// Both ingress controllers are asked, because there is no portable series: Traefik exports
// traefik_service_requests_total and nginx exports nginx_ingress_controller_requests, and
// each has two label spellings depending on how Prometheus was configured to scrape it. The
// `or` chain returns whichever exists.
//
// increase() rather than rate(): the question is "were there any requests at all", and a
// rate low enough to round to zero over a long window is not the same as none.
func RequestRateQuery(namespace, app string, window time.Duration) string {
	w := promDuration(window)
	traefikSvc := fmt.Sprintf(`%s-%s-.*@kubernetes`, namespace, app)

	traefik := fmt.Sprintf(
		`sum(increase(traefik_service_requests_total{exported_service=~"%s"}[%s])) `+
			`or sum(increase(traefik_service_requests_total{service=~"%s"}[%s]))`,
		traefikSvc, w, traefikSvc, w)

	nginx := fmt.Sprintf(
		`sum(increase(nginx_ingress_controller_requests{exported_namespace="%s",ingress="%s"}[%s])) `+
			`or sum(increase(nginx_ingress_controller_requests{namespace="%s",ingress="%s"}[%s]))`,
		namespace, app, w, namespace, app, w)

	return traefik + " or " + nginx
}

// promDuration renders a duration the way PromQL wants it.
func promDuration(d time.Duration) string {
	if d < time.Minute {
		d = time.Minute
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
