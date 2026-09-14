package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// httptest is used here and nowhere else in this repo.
//
// The rest of the codebase tests pure functions, and that is the right default. But the v2
// authentication dance is a three-request protocol -- unauthenticated request, 401 carrying
// a token service, token fetch, retry -- and its correctness is the order and content of
// those requests, which no table test can express. The package is pure HTTP with no
// Kubernetes or database dependency, so the server fits in the test.

func TestParseWWWAuthenticate(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		wantRealm  string
		wantParams map[string]string
	}{
		{
			"docker hub style",
			`Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`,
			"https://auth.docker.io/token",
			map[string]string{"service": "registry.docker.io"},
		},
		{
			// A scope contains commas inside its quotes, which is why this is not a split
			// on commas.
			"scope with commas",
			`Bearer realm="https://auth.example.com/token",service="reg",scope="repository:lib/app:pull,push"`,
			"https://auth.example.com/token",
			map[string]string{"service": "reg", "scope": "repository:lib/app:pull,push"},
		},
		{
			"spaces after commas",
			`Bearer realm="https://auth.example.com/token", service="reg"`,
			"https://auth.example.com/token",
			map[string]string{"service": "reg"},
		},
		{"basic is not bearer", `Basic realm="registry"`, "", map[string]string{}},
		{"empty", "", "", map[string]string{}},
		{"no parameters", "Bearer", "", map[string]string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			realm, params := ParseWWWAuthenticate(tc.header)
			if realm != tc.wantRealm {
				t.Errorf("realm = %q, want %q", realm, tc.wantRealm)
			}
			for k, want := range tc.wantParams {
				if params[k] != want {
					t.Errorf("params[%q] = %q, want %q", k, params[k], want)
				}
			}
		})
	}
}

// The whole point of the handshake: an anonymous request is refused, the refusal names a
// token service, and the retry carries the token that service issued.
func TestBearerTokenHandshake(t *testing.T) {
	var sawBasicAuthOnToken bool
	var sawBearerOnRetry string

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && u == "alice" && p == "s3cret" {
			sawBasicAuthOnToken = true
		}
		if r.URL.Query().Get("service") != "my-registry" {
			t.Errorf("token request lost the service parameter: %q", r.URL.RawQuery)
		}
		w.Write([]byte(`{"token":"issued-token"}`))
	})
	mux.HandleFunc("/v2/_catalog", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			sawBearerOnRetry = strings.TrimPrefix(auth, "Bearer ")
			w.Write([]byte(`{"repositories":["lib/app","lib/api"]}`))
			return
		}
		// First contact: refuse, and say where to get a token.
		w.Header().Set("WWW-Authenticate",
			`Bearer realm="`+serverURL(r)+`/token",service="my-registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient()
	repos, err := c.ListRepositories(context.Background(), Credentials{
		Registry: srv.URL,
		Username: "alice",
		Password: "s3cret",
		Flavor:   FlavorGenericV2,
	})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}

	if !sawBasicAuthOnToken {
		t.Error("the token request did not carry basic auth; the token service has no other way to identify the caller")
	}
	if sawBearerOnRetry != "issued-token" {
		t.Errorf("the retry carried %q, want the issued token", sawBearerOnRetry)
	}
	if len(repos) != 2 || repos[0] != "lib/app" {
		t.Errorf("repositories = %v", repos)
	}
}

// A registry that accepts basic auth directly must not be dragged through a token exchange
// it never offered.
func TestBasicAuthRegistryNeedsNoToken(t *testing.T) {
	tokenCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) { tokenCalls++ })
	mux.HandleFunc("/v2/_catalog", func(w http.ResponseWriter, r *http.Request) {
		if u, _, ok := r.BasicAuth(); !ok || u != "alice" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"repositories":["only/one"]}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient()
	repos, err := c.ListRepositories(context.Background(), Credentials{
		Registry: srv.URL,
		Username: "alice", Password: "s3cret", Flavor: FlavorGenericV2,
	})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if tokenCalls != 0 {
		t.Errorf("token endpoint was called %d times for a basic-auth registry", tokenCalls)
	}
	if len(repos) != 1 {
		t.Errorf("repositories = %v", repos)
	}
}

// Ping has to tell "cannot reach this host" apart from "this host rejected these
// credentials" -- they send you to different places, and one message for both sends you to
// the wrong one half the time.
func TestPingDistinguishesUnreachableFromRejected(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		err := NewClient().Ping(context.Background(), Credentials{
			Registry: srv.URL, Username: "alice", Password: "wrong",
		})
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Errorf("error = %v, want it to say the credentials were rejected", err)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		// A port nothing is listening on.
		err := NewClient().Ping(context.Background(), Credentials{
			Registry: "http://127.0.0.1:1", Username: "alice", Password: "s3cret",
		})
		if err == nil || !strings.Contains(err.Error(), "reach") {
			t.Errorf("error = %v, want it to say the registry could not be reached", err)
		}
	})

	t.Run("reachable and accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := NewClient().Ping(context.Background(), Credentials{
			Registry: srv.URL, Username: "alice", Password: "s3cret",
		}); err != nil {
			t.Errorf("Ping on a working registry errored: %v", err)
		}
	})
}

// Harbor's project API is walked in preference to _catalog, which some deployments disable.
func TestHarborWalksProjects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2.0/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"library"},{"name":"team-a"}]`))
	})
	mux.HandleFunc("/api/v2.0/projects/library/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"library/nginx"}]`))
	})
	mux.HandleFunc("/api/v2.0/projects/team-a/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"team-a/api"},{"name":"team-a/web"}]`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	repos, err := NewClient().ListRepositories(context.Background(), Credentials{
		Registry: srv.URL,
		Username: "admin", Password: "s3cret", Flavor: FlavorHarbor,
	})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 3 {
		t.Errorf("repositories = %v, want all three across both projects", repos)
	}
}

// One unreadable project must not empty the whole list -- a credential scoped to one
// project is normal, not an error.
func TestHarborToleratesAnUnreadableProject(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2.0/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"readable"},{"name":"forbidden"}]`))
	})
	mux.HandleFunc("/api/v2.0/projects/readable/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"readable/app"}]`))
	})
	mux.HandleFunc("/api/v2.0/projects/forbidden/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	repos, err := NewClient().ListRepositories(context.Background(), Credentials{
		Registry: srv.URL, Flavor: FlavorHarbor,
	})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(repos) != 1 || repos[0] != "readable/app" {
		t.Errorf("repositories = %v, want just the readable one", repos)
	}
}

func TestListTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/lib/app/tags/list" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Write([]byte(`{"name":"lib/app","tags":["v1","v2","latest"]}`))
	}))
	defer srv.Close()

	tags, err := NewClient().ListTags(context.Background(), Credentials{
		Registry: srv.URL,
	}, "lib/app")
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 3 {
		t.Errorf("tags = %v", tags)
	}
}

// The UI calls these on every form render, so a second call inside the window must not
// reach the registry again.
func TestResultsAreCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"repositories":["a/b"]}`))
	}))
	defer srv.Close()

	c := NewClient()
	creds := Credentials{Registry: srv.URL, Flavor: FlavorGenericV2}

	for i := 0; i < 3; i++ {
		if _, err := c.ListRepositories(context.Background(), creds); err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("registry was called %d times for three identical listings, want 1", calls)
	}
}

func TestDetectFlavor(t *testing.T) {
	for registry, want := range map[string]string{
		"docker.io":                   FlavorDockerHub,
		"https://index.docker.io/v1/": FlavorDockerHub,
		"ghcr.io":                     FlavorGHCR,
		"harbor.example.com":          FlavorHarbor,
		"registry.internal:5000":      FlavorGenericV2,
		"quay.io":                     FlavorGenericV2,
	} {
		if got := DetectFlavor(registry); got != want {
			t.Errorf("DetectFlavor(%q) = %q, want %q", registry, got, want)
		}
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
