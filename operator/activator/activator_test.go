package activator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// httptest is used here for the same reason it is used in the registry client: the
// behaviour worth testing is a sequence over time -- wake, then wait, then proxy -- with
// concurrency and a timeout, none of which a table test can express. The package has no
// Kubernetes dependency; the cluster side is behind the Waker interface.

type fakeWaker struct {
	mu sync.Mutex

	wakes      int32
	readyAfter int32 // become ready after this many readiness checks
	checks     int32

	wakeErr  error
	readyErr error
	resolve  func(host string) (Target, bool)
}

func (f *fakeWaker) Wake(ctx context.Context, namespace, app string) error {
	atomic.AddInt32(&f.wakes, 1)
	return f.wakeErr
}

func (f *fakeWaker) Ready(ctx context.Context, namespace, app string) (bool, error) {
	n := atomic.AddInt32(&f.checks, 1)
	if f.readyErr != nil {
		return false, f.readyErr
	}
	return n > atomic.LoadInt32(&f.readyAfter), nil
}

func (f *fakeWaker) Resolve(ctx context.Context, host string) (Target, bool) {
	if f.resolve != nil {
		return f.resolve(host)
	}
	return Target{App: "web", Port: 80}, true
}

// The whole point: a request to a sleeping app arrives, the app is woken, and the request is
// answered by the app rather than failing.
func TestRequestWakesAndProxies(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("served by the app"))
	}))
	defer backend.Close()

	waker := &fakeWaker{readyAfter: 2}
	a := New(waker, Options{Namespace: "acme-production", PollInterval: time.Millisecond})
	// Point the proxy at the test backend rather than a cluster DNS name.
	a.targetURL = func(string, int32) string { return backend.URL }

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://web.example.com/", nil)
	a.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "served by the app" {
		t.Errorf("body = %q, want the app's response", rec.Body.String())
	}
	if waker.wakes != 1 {
		t.Errorf("woke %d times, want 1", waker.wakes)
	}
}

// A sleeping app that suddenly receives a burst should be woken once. Each wake is a write
// to the Kubernetes API, and fifty of them for one app is both wasteful and a good way to
// get rate limited at exactly the wrong moment.
func TestConcurrentRequestsWakeOnce(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	waker := &fakeWaker{readyAfter: 3}
	a := New(waker, Options{Namespace: "ns", PollInterval: time.Millisecond})
	a.targetURL = func(string, int32) string { return backend.URL }

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d", rec.Code)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&waker.wakes); got > 3 {
		t.Errorf("woke %d times for 20 concurrent requests; they should collapse", got)
	}
}

// An app that never becomes ready must not hold the request forever. A hung connection is
// worse than an error: the caller's own timeout decides what happens, and nothing says why.
func TestGivesUpWithRetryAfter(t *testing.T) {
	waker := &fakeWaker{readyAfter: 1 << 30} // never ready
	a := New(waker, Options{
		Namespace: "ns", WakeTimeout: 30 * time.Millisecond, PollInterval: time.Millisecond,
	})

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After; a client that retries immediately hammers a starting app")
	}
}

// A wake that fails is reported, not waited out for the full timeout.
func TestWakeFailureIsReported(t *testing.T) {
	// readyAfter must be high enough that the app is never ready, or this does not test
	// what it says. The handler checks readiness first and proxies when the app is already
	// up, so a fake that reports ready immediately never reaches the wake at all -- it
	// proxied to web.ns.svc.cluster.local instead, and the assertion below was really
	// measuring how fast the local resolver rejects an unresolvable name. On a machine
	// where that takes five seconds rather than microseconds, the test failed with a
	// message about readiness timeouts and nothing to do with the bug it guards.
	waker := &fakeWaker{wakeErr: errors.New("forbidden"), readyAfter: 1 << 30}
	a := New(waker, Options{Namespace: "ns", WakeTimeout: time.Second, PollInterval: time.Millisecond})

	start := time.Now()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("a failed wake waited for the readiness timeout instead of returning")
	}
}

// An ingress pointing here for an app that no longer exists should say so, rather than
// producing a blank 502 from a proxy to a name that does not resolve.
func TestUnknownHostIs404(t *testing.T) {
	waker := &fakeWaker{resolve: func(string) (Target, bool) { return Target{}, false }}
	a := New(waker, Options{Namespace: "ns"})

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://gone.example.com/", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// The activator's own health must not sit on an application path.
//
// It used to be registered at /healthz on the same mux as application traffic, which meant
// an app whose health endpoint is /healthz never reached the app: the activator answered
// 200 for it whether the app was asleep, awake, or failing. A health check that always
// reports healthy is worse than none.
func TestProbeIsNotOnTheApplicationPort(t *testing.T) {
	waker := &fakeWaker{
		readyAfter: 1 << 30,
		resolve: func(string) (Target, bool) {
			return Target{App: "web", Port: 80}, true
		},
	}
	a := New(waker, Options{Namespace: "ns", WakeTimeout: 20 * time.Millisecond, PollInterval: time.Millisecond})

	// /healthz on the application handler is the app's path, so it is treated like any
	// other request: the app is asleep and not in the no-wake list, so it is woken.
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/healthz", nil))

	if atomic.LoadInt32(&waker.wakes) == 0 {
		t.Error("/healthz was answered by the activator instead of belonging to the app")
	}

	// The activator's own probe lives on its own handler, and wakes nothing.
	before := atomic.LoadInt32(&waker.wakes)
	probeRec := httptest.NewRecorder()
	ProbeHandler().ServeHTTP(probeRec, httptest.NewRequest("GET", "http://127.0.0.1:8081/healthz", nil))

	if probeRec.Code != http.StatusOK {
		t.Errorf("probe status = %d, want 200", probeRec.Code)
	}
	if atomic.LoadInt32(&waker.wakes) != before {
		t.Error("the activator's own health check woke an app")
	}
}

// A browser sends Host with a port on non-default ports; an ingress host never has one. Left
// unstripped they never match and every request 404s.
func TestHostPortIsStripped(t *testing.T) {
	cases := map[string]string{
		"web.example.com":      "web.example.com",
		"web.example.com:8080": "web.example.com",
		"[::1]:8080":           "[::1]",
		"[::1]":                "[::1]",
		"localhost:3000":       "localhost",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

// The reason no-wake paths exist.
//
// An uptime monitor polling the public URL every minute would otherwise wake the app on its
// first poll, and the app's own traffic would then keep the request rate above zero -- so it
// would never sleep again, and scale-to-zero would look simply broken.
func TestHealthCheckDoesNotWakeASleepingApp(t *testing.T) {
	waker := &fakeWaker{
		readyAfter: 1 << 30, // never ready: the app is asleep and stays that way
		resolve: func(string) (Target, bool) {
			return Target{App: "web", Port: 80, NoWakePaths: []string{"/healthz"}}, true
		},
	}
	a := New(waker, Options{Namespace: "ns", WakeTimeout: time.Second, PollInterval: time.Millisecond})

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 -- an asleep app is in the state it was asked to be in, "+
			"and a monitor reading this as an outage would page somebody for a working system", rec.Code)
	}
	if atomic.LoadInt32(&waker.wakes) != 0 {
		t.Error("the health check woke the app; scale-to-zero would never stick under monitoring")
	}
	if rec.Header().Get("X-Vesta-Status") != "asleep" {
		t.Error("nothing in the response distinguishes asleep from genuinely serving")
	}
}

// A path that is not listed still wakes the app, or nothing ever would.
func TestOtherPathsStillWake(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("app"))
	}))
	defer backend.Close()

	waker := &fakeWaker{
		readyAfter: 1,
		resolve: func(string) (Target, bool) {
			return Target{App: "web", Port: 80, NoWakePaths: []string{"/healthz"}}, true
		},
	}
	a := New(waker, Options{Namespace: "ns", PollInterval: time.Millisecond})
	a.targetURL = func(string, int32) string { return backend.URL }

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "app" {
		t.Errorf("status = %d body = %q, want the app's own response", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&waker.wakes) == 0 {
		t.Error("an ordinary request did not wake the app")
	}
}

// Once the app is up, a health check must reach the app. Answering it here would report that
// the activator is fine while the app behind it is failing -- which is worse than not having
// a health check at all.
func TestHealthCheckReachesARunningApp(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusInternalServerError) // the app is up but unhealthy
	}))
	defer backend.Close()

	waker := &fakeWaker{
		readyAfter: 0, // ready immediately
		resolve: func(string) (Target, bool) {
			return Target{App: "web", Port: 80, NoWakePaths: []string{"/healthz"}}, true
		},
	}
	a := New(waker, Options{Namespace: "ns", PollInterval: time.Millisecond})
	a.targetURL = func(string, int32) string { return backend.URL }

	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://web.example.com/healthz", nil))

	if gotPath != "/healthz" {
		t.Errorf("the health check did not reach the app (path seen: %q)", gotPath)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want the app's own 500 -- reporting 200 for a failing app "+
			"is worse than having no health check", rec.Code)
	}
}

func TestMatchesNoWake(t *testing.T) {
	patterns := []string{"/healthz", "/status/", "/internal/*", "  ", ""}

	cases := map[string]bool{
		"/healthz":          true,
		"/healthz/":         true, // same endpoint to everyone except a string comparison
		"healthz":           true,
		"/status":           true,
		"/status/":          true,
		"/internal/metrics": true,
		"/internal/":        true,
		"/healthzz":         false,
		"/health":           false,
		"/":                 false,
		"/api/healthz":      false,
	}

	for path, want := range cases {
		if got := matchesNoWake(path, patterns); got != want {
			t.Errorf("matchesNoWake(%q) = %v, want %v", path, got, want)
		}
	}

	if matchesNoWake("/healthz", nil) {
		t.Error("an app with no no-wake paths matched one")
	}

	// Listing "/" switches wake-on-traffic off in practice. That is the user's call, but it
	// must do what it says rather than being quietly ignored.
	if !matchesNoWake("/anything", []string{"/*"}) {
		t.Error(`a "/*" pattern did not match everything`)
	}
}
