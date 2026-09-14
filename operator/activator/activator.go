// Package activator stands in for sleeping apps.
//
// A scaled-to-zero app has no pods, so its Service resolves to nothing and every request to
// it fails. While an app is down the operator points its Ingress here instead. This holds
// the request, wakes the app, waits for a pod to serve, and proxies through — so the
// requester sees a slow response rather than an error, and never learns the app was asleep.
package activator

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Waker starts a sleeping app and reports when it can serve.
//
// An interface so the HTTP behaviour can be tested without a cluster — which matters,
// because the interesting parts here are concurrency and timeouts, not Kubernetes.
type Waker interface {
	// Wake asks for an app to run. It must be safe to call for an app already running.
	Wake(ctx context.Context, namespace, app string) error
	// Ready reports whether the app has at least one pod serving.
	Ready(ctx context.Context, namespace, app string) (bool, error)
	// Resolve maps a request's Host header to the app that serves it.
	Resolve(ctx context.Context, host string) (Target, bool)
}

// Target is where a request for a given host should go, and how to treat it.
type Target struct {
	App  string
	Port int32
	// NoWakePaths are answered here while the app is down rather than waking it.
	NoWakePaths []string
}

// Options configure an Activator.
type Options struct {
	Namespace string
	// WakeTimeout bounds how long a request is held. Past it the requester gets a 503 with
	// Retry-After rather than a connection that hangs until their own client gives up.
	WakeTimeout time.Duration
	// PollInterval is how often readiness is rechecked while holding a request.
	PollInterval time.Duration
}

func (o *Options) setDefaults() {
	if o.WakeTimeout == 0 {
		o.WakeTimeout = 60 * time.Second
	}
	if o.PollInterval == 0 {
		o.PollInterval = 250 * time.Millisecond
	}
}

// Activator is the HTTP handler.
type Activator struct {
	waker Waker
	opts  Options

	// inflight collapses concurrent wakes for one app. A sleeping app that suddenly
	// receives fifty requests should be woken once, not fifty times -- each of those being
	// a write to the Kubernetes API.
	mu       sync.Mutex
	inflight map[string]*wakeCall

	// targetURL builds the address to proxy to. A field so a test can point it at a local
	// server instead of a cluster DNS name it cannot resolve.
	targetURL func(app string, port int32) string
}

type wakeCall struct {
	done chan struct{}
	err  error
}

func New(w Waker, opts Options) *Activator {
	opts.setDefaults()
	a := &Activator{waker: w, opts: opts, inflight: map[string]*wakeCall{}}
	a.targetURL = func(app string, port int32) string {
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", app, a.opts.Namespace, port)
	}
	return a
}

// Handler serves application traffic. Every path belongs to the app.
//
// The activator's own readiness probe is deliberately NOT here -- see ProbeHandler. Putting
// it on this mux meant an app whose health endpoint happened to be /healthz never reached
// the app at all: the activator answered 200 for it, awake or asleep, so a failing app
// reported healthy.
func (a *Activator) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.serve)
	return mux
}

// ProbeHandler is the activator's own health, served on a separate port.
//
// A separate port rather than a reserved path, because there is no path an app cannot
// plausibly want. Kubelet probes the pod directly, so it never needs to travel the route
// application traffic takes.
func ProbeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (a *Activator) serve(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)

	target, ok := a.waker.Resolve(r.Context(), host)
	if !ok {
		// Nothing here answers for that host. Saying so is better than a blank 502: it is
		// usually an ingress pointing at the activator for an app that was deleted.
		http.Error(w, "no application is configured for "+host, http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), a.opts.WakeTimeout)
	defer cancel()

	// Readiness first, and only wake what is not already up.
	//
	// This matters more than it looks. The ingress keeps pointing here until the operator's
	// next reconcile notices the app is serving, so for up to thirty seconds after a wake
	// every request still arrives at the activator. Waking unconditionally would make each
	// of those a write to the Kubernetes API -- a burst of traffic on a just-woken app
	// becoming a burst of writes, which is a good way to get rate limited at the worst
	// possible moment.
	if ready, err := a.waker.Ready(ctx, a.opts.Namespace, target.App); err == nil && ready {
		a.proxy(w, r, target)
		return
	}

	// The app is down, and this is a path it should not be woken for.
	//
	// Without this, scale-to-zero does not survive contact with monitoring: an uptime check
	// polling every minute wakes the app, and the app's own traffic then keeps it awake, so
	// it never sleeps again and the feature looks broken. Checked only while the app is
	// down -- once it is up these proxy through like anything else, so a health check
	// against a live app still reports on the app rather than on this.
	if matchesNoWake(r.URL.Path, target.NoWakePaths) {
		asleepOK(w, target.App)
		return
	}

	if err := a.wakeOnce(ctx, target.App); err != nil {
		log.Printf("[activator] waking %s/%s: %v", a.opts.Namespace, target.App, err)
		retryLater(w, "the application could not be started")
		return
	}

	if err := a.waitReady(ctx, target.App); err != nil {
		log.Printf("[activator] waiting for %s/%s: %v", a.opts.Namespace, target.App, err)
		retryLater(w, "the application is starting but is not ready yet")
		return
	}

	a.proxy(w, r, target)
}

// wakeOnce asks for the app to run, collapsing concurrent callers onto one request.
func (a *Activator) wakeOnce(ctx context.Context, app string) error {
	a.mu.Lock()
	if c, ok := a.inflight[app]; ok {
		a.mu.Unlock()
		select {
		case <-c.done:
			return c.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := &wakeCall{done: make(chan struct{})}
	a.inflight[app] = c
	a.mu.Unlock()

	c.err = a.waker.Wake(ctx, a.opts.Namespace, app)

	a.mu.Lock()
	delete(a.inflight, app)
	a.mu.Unlock()
	close(c.done)

	return c.err
}

func (a *Activator) waitReady(ctx context.Context, app string) error {
	ticker := time.NewTicker(a.opts.PollInterval)
	defer ticker.Stop()

	for {
		ready, err := a.waker.Ready(ctx, a.opts.Namespace, app)
		if err == nil && ready {
			return nil
		}

		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("gave up waiting: %w", err)
			}
			return fmt.Errorf("gave up waiting for %s to become ready", app)
		case <-ticker.C:
		}
	}
}

// proxy forwards to the app's own Service.
//
// Directly, not back through the ingress: the ingress still points here until the next
// reconcile notices the app is up, so going through it would route straight back to this
// activator and loop.
func (a *Activator) proxy(w http.ResponseWriter, r *http.Request, t Target) {
	target, err := url.Parse(a.targetURL(t.App, t.Port))
	if err != nil {
		retryLater(w, "the application address could not be resolved")
		return
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[activator] proxying to %s: %v", target.Host, err)
		retryLater(w, "the application started but did not accept the request")
	}
	// httputil handles Upgrade, so websockets survive being woken.
	rp.ServeHTTP(w, r)
}

// retryLater answers with a 503 the caller can act on.
//
// Retry-After matters: without it a client that retries immediately hammers a starting app,
// and one that does not retry reports a hard failure for something that was merely slow.
func retryLater(w http.ResponseWriter, message string) {
	w.Header().Set("Retry-After", "5")
	http.Error(w, message, http.StatusServiceUnavailable)
}

// hostOnly strips any port from a Host header. Browsers include one on non-default ports,
// and the ingress host never does, so comparing them raw never matches.
func hostOnly(host string) string {
	if i := strings.LastIndex(host, ":"); i > strings.LastIndex(host, "]") {
		return host[:i]
	}
	return host
}

// asleepOK answers a no-wake request without starting the app.
//
// 200, because the app is not broken -- it is scaled to zero, which is the state it was
// asked to be in, and a monitor that reads this as an outage would page somebody for a
// working system. The header says what actually happened, so a check that wants to
// distinguish the two can.
func asleepOK(w http.ResponseWriter, app string) {
	w.Header().Set("X-Vesta-Status", "asleep")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "%s is scaled to zero and will start on the next request\n", app)
}

// matchesNoWake reports whether a path is one the app should not be woken for.
//
// Exact by default, with a trailing * for a prefix. A trailing slash is ignored on both
// sides, because "/health" and "/health/" are the same endpoint to everyone except a string
// comparison -- and getting that wrong means the monitor wakes the app anyway, silently.
func matchesNoWake(path string, patterns []string) bool {
	normalised := normalisePath(path)

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if prefix, found := strings.CutSuffix(pattern, "*"); found {
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if normalised == normalisePath(pattern) {
			return true
		}
	}
	return false
}

func normalisePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}
