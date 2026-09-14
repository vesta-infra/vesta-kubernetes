package scope

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestResolveCaches(t *testing.T) {
	calls := 0
	r := NewResolver(func(ctx context.Context, appID string) (Scope, error) {
		calls++
		return Scope{Project: "acme"}, nil
	})

	for i := 0; i < 5; i++ {
		s, err := r.Resolve(context.Background(), "web")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if s.Project != "acme" {
			t.Fatalf("Project = %q", s.Project)
		}
	}
	if calls != 1 {
		t.Errorf("looked up %d times for five resolves, want 1 -- every app route would otherwise read from the API server", calls)
	}
}

// A failure must not be cached as long as a success. A missing app and a transient API error
// look identical here, and holding the error for the full lifetime would turn a blip into
// half a minute of refused requests.
func TestFailuresExpireSooner(t *testing.T) {
	r := NewResolver(func(ctx context.Context, appID string) (Scope, error) {
		return Scope{}, errors.New("nope")
	})
	if r.negTTL >= r.ttl {
		t.Fatalf("negative TTL %v is not shorter than the positive TTL %v", r.negTTL, r.ttl)
	}

	now := time.Now()
	r.now = func() time.Time { return now }

	if _, err := r.Resolve(context.Background(), "web"); err == nil {
		t.Fatal("expected an error")
	}

	calls := 0
	r.lookup = func(ctx context.Context, appID string) (Scope, error) {
		calls++
		return Scope{Project: "acme"}, nil
	}

	// Still inside the negative window: the cached failure stands.
	now = now.Add(r.negTTL - time.Second)
	if _, err := r.Resolve(context.Background(), "web"); err == nil {
		t.Error("a cached failure expired early")
	}

	// Past it: the app is looked up again and recovers.
	now = now.Add(2 * time.Second)
	s, err := r.Resolve(context.Background(), "web")
	if err != nil {
		t.Fatalf("Resolve after the negative window: %v", err)
	}
	if s.Project != "acme" || calls != 1 {
		t.Errorf("did not retry after the negative window (project=%q calls=%d)", s.Project, calls)
	}
}

func TestEntriesExpire(t *testing.T) {
	calls := 0
	r := NewResolver(func(ctx context.Context, appID string) (Scope, error) {
		calls++
		return Scope{Project: "acme"}, nil
	})

	now := time.Now()
	r.now = func() time.Time { return now }

	r.Resolve(context.Background(), "web")
	now = now.Add(r.ttl + time.Second)
	r.Resolve(context.Background(), "web")

	if calls != 2 {
		t.Errorf("looked up %d times across an expiry, want 2", calls)
	}
}

// An app that was just moved, renamed or deleted must not be judged against a stale project.
func TestInvalidate(t *testing.T) {
	calls := 0
	r := NewResolver(func(ctx context.Context, appID string) (Scope, error) {
		calls++
		return Scope{Project: "acme"}, nil
	})

	r.Resolve(context.Background(), "web")
	r.Invalidate("web")
	r.Resolve(context.Background(), "web")

	if calls != 2 {
		t.Errorf("looked up %d times across an invalidation, want 2", calls)
	}
}

// One page renders several panels for one app. Those requests arrive together, and they
// should cause one read rather than one each.
func TestConcurrentMissesCollapse(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	release := make(chan struct{})
	r := NewResolver(func(ctx context.Context, appID string) (Scope, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release // hold the first lookup open so the others pile up behind it
		return Scope{Project: "acme"}, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := r.Resolve(context.Background(), "web")
			if err != nil || s.Project != "acme" {
				t.Errorf("Resolve = (%+v, %v)", s, err)
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("eight concurrent resolves caused %d lookups, want 1", calls)
	}
}

// An empty app id is not an app. Resolving it must not reach the cluster, and must not
// produce a scope that could satisfy a gate.
func TestEmptyAppIDIsNotLookedUp(t *testing.T) {
	r := NewResolver(func(ctx context.Context, appID string) (Scope, error) {
		t.Fatal("looked up an empty app id")
		return Scope{}, nil
	})

	s, err := r.Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}
	if s.Project != "" {
		t.Errorf("empty app id produced project %q", s.Project)
	}
	if r.Len() != 0 {
		t.Error("an empty app id was cached")
	}
}
