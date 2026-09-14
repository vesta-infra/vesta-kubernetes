// Package scope answers "which project does this app belong to".
//
// It exists because of an awkward fact about Vesta's routes: almost everything is keyed on
// :appId, and an app's project lives inside its custom resource. A gate that needs the
// project therefore needs a Kubernetes read, and doing one per request on every app route
// would put the API server in the path of every page load.
package scope

import (
	"context"
	"sync"
	"time"
)

// Scope is what a gate needs to know about an app.
type Scope struct {
	Project      string
	Environments []string
}

// Resolver caches app-to-project lookups.
//
// The lookup is a func rather than a Kubernetes client so the cache can be tested with no
// cluster and no fake -- which matters, because the interesting behaviour here is expiry,
// negative caching and collapsing concurrent misses, none of which involve Kubernetes.
type Resolver struct {
	lookup func(ctx context.Context, appID string) (Scope, error)

	mu      sync.Mutex
	entries map[string]entry
	// inflight collapses concurrent misses for the same app onto one lookup. A page that
	// fires six requests for one app should cause one read, not six.
	inflight map[string]*call

	ttl    time.Duration
	negTTL time.Duration
	now    func() time.Time
}

type entry struct {
	scope   Scope
	err     error
	expires time.Time
}

type call struct {
	done  chan struct{}
	scope Scope
	err   error
}

// Cache lifetimes.
//
// A positive result is stable -- an app's project never changes, since moving one means
// creating another -- so the only reason to expire it at all is to bound memory and pick up
// a deletion. A negative result expires much sooner: it is usually a genuinely missing app,
// but it is also what a transient API error looks like, and caching that for half a minute
// would turn a blip into an outage.
const (
	DefaultTTL         = 30 * time.Second
	DefaultNegativeTTL = 5 * time.Second
)

func NewResolver(lookup func(ctx context.Context, appID string) (Scope, error)) *Resolver {
	return &Resolver{
		lookup:   lookup,
		entries:  map[string]entry{},
		inflight: map[string]*call{},
		ttl:      DefaultTTL,
		negTTL:   DefaultNegativeTTL,
		now:      time.Now,
	}
}

// Resolve returns an app's scope, from cache when it can.
func (r *Resolver) Resolve(ctx context.Context, appID string) (Scope, error) {
	if appID == "" {
		return Scope{}, nil
	}

	r.mu.Lock()
	if e, ok := r.entries[appID]; ok && r.now().Before(e.expires) {
		r.mu.Unlock()
		return e.scope, e.err
	}

	if c, ok := r.inflight[appID]; ok {
		r.mu.Unlock()
		<-c.done
		return c.scope, c.err
	}

	c := &call{done: make(chan struct{})}
	r.inflight[appID] = c
	r.mu.Unlock()

	c.scope, c.err = r.lookup(ctx, appID)

	r.mu.Lock()
	ttl := r.ttl
	if c.err != nil {
		ttl = r.negTTL
	}
	r.entries[appID] = entry{scope: c.scope, err: c.err, expires: r.now().Add(ttl)}
	delete(r.inflight, appID)
	r.mu.Unlock()

	close(c.done)
	return c.scope, c.err
}

// Invalidate drops an app from the cache.
//
// Called when an app is created, changed, cloned or deleted, so a permission decision never
// runs against a project the app no longer belongs to.
func (r *Resolver) Invalidate(appID string) {
	r.mu.Lock()
	delete(r.entries, appID)
	r.mu.Unlock()
}

// Len reports how many entries are cached, for tests.
func (r *Resolver) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
