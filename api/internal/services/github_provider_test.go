package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"

	"kubernetes.getvesta.sh/api/internal/git"
)

// refs/tags/v1 used to become branch "v1", because the old code sliced ref[11:] behind a
// len(ref) > 11 guard. A tag push then matched an environment watching a branch of that
// name and deployed it.
func TestBranchFromRef(t *testing.T) {
	cases := map[string]string{
		"refs/heads/main":           "main",
		"refs/heads/release/1.2":    "release/1.2",
		"refs/heads/feature-x":      "feature-x",
		"refs/tags/v1":              "",
		"refs/tags/refs/heads/main": "",
		"refs/pull/12/merge":        "",
		"refs/heads/":               "",
		"main":                      "",
		"":                          "",
		"refs/remotes/origin/main":  "",
		"refs/heads/v1":             "v1",
	}
	for ref, want := range cases {
		t.Run(ref, func(t *testing.T) {
			if got := branchFromRef(ref); got != want {
				t.Errorf("branchFromRef(%q) = %q, want %q", ref, got, want)
			}
		})
	}
}

// The neutral vocabulary has states GitHub does not, so every one must map to something
// GitHub accepts -- an unmapped state would be rejected by the API and the check would stay
// yellow forever, which is the behaviour this whole area is meant to fix.
func TestGitHubStateMapping(t *testing.T) {
	valid := map[string]bool{"pending": true, "success": true, "failure": true, "error": true}

	for _, s := range []git.BuildState{
		git.StatePending, git.StateRunning, git.StateSuccess,
		git.StateFailed, git.StateError, git.StateCanceled,
	} {
		got := githubState(s)
		if !valid[got] {
			t.Errorf("githubState(%v) = %q, which GitHub does not accept", s, got)
		}
	}

	// The two that matter for reading a PR: a finished build must not still look pending.
	if githubState(git.StateSuccess) == "pending" || githubState(git.StateFailed) == "pending" {
		t.Error("a finished build mapped to pending")
	}
	if githubState(git.StateFailed) != "failure" {
		t.Errorf("githubState(failed) = %q, want %q -- GitHub spells it \"failure\"",
			githubState(git.StateFailed), "failure")
	}
}

// The credential carries how to authenticate, not just the token. Losing either half sends
// us back to every call site guessing, which is how Bearer and x-access-token came to be
// hardcoded in two different files.
func TestGitHubCredentialCarriesItsScheme(t *testing.T) {
	c := githubCredential("ghs_token")
	if c.Scheme != "Bearer" {
		t.Errorf("Scheme = %q, want Bearer", c.Scheme)
	}
	if c.Username != "x-access-token" {
		t.Errorf("Username = %q, want x-access-token -- it is the clone username", c.Username)
	}
	if c.Header() != "Bearer ghs_token" {
		t.Errorf("Header() = %q", c.Header())
	}
	if c.Empty() {
		t.Error("a credential with a token reported itself empty")
	}

	if empty := githubCredential(""); !empty.Empty() || empty.Header() != "" {
		t.Error("an absent token must produce an empty credential and no header")
	}
}

func TestVerifyWebhook(t *testing.T) {
	p := &GitHubProvider{}
	body := []byte(`{"ref":"refs/heads/main"}`)
	const secret = "s3cret"

	sign := func(key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	cases := []struct {
		name    string
		header  string
		secret  string
		wantErr bool
	}{
		{"valid", sign(secret), secret, false},
		{"wrong key", sign("other"), secret, true},
		{"garbage", "sha256=zzz", secret, true},
		// A provider must never be the thing that decides an unsigned delivery is fine.
		// The handler owns that decision, because only it knows about the upgrade flag.
		{"absent signature", "", secret, true},
		{"no secret configured", sign(secret), "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.header != "" {
				h.Set("X-Hub-Signature-256", tc.header)
			}
			err := p.VerifyWebhook(h, body, tc.secret)
			if (err != nil) != tc.wantErr {
				t.Errorf("VerifyWebhook() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestParsePush(t *testing.T) {
	p := &GitHubProvider{}
	conn := git.Connection{Provider: git.ProviderGitHub}

	t.Run("a push", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-GitHub-Event", "push")
		body := []byte(`{
			"ref": "refs/heads/main",
			"repository": {"full_name": "Acme/Web"},
			"head_commit": {"id": "abcdef1234567890"},
			"pusher": {"name": "someone"}
		}`)

		ev, err := p.ParsePush(h, body, conn)
		if err != nil {
			t.Fatalf("ParsePush: %v", err)
		}
		if ev.Branch != "main" {
			t.Errorf("Branch = %q, want main", ev.Branch)
		}
		if ev.CommitSHA != "abcdef1234567890" {
			t.Errorf("CommitSHA = %q", ev.CommitSHA)
		}
		// Normalised on the way in, so the matcher never sees the raw casing.
		if ev.Repo.Path != "acme/web" || ev.Repo.Host != "github.com" {
			t.Errorf("Repo = %+v, want acme/web on github.com", ev.Repo)
		}
	})

	t.Run("a non-push is not a failure", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-GitHub-Event", "pull_request")
		if _, err := p.ParsePush(h, []byte(`{}`), conn); !errors.Is(err, git.ErrNotAPush) {
			t.Errorf("error = %v, want ErrNotAPush -- a pull_request delivery is valid, just not ours", err)
		}
	})

	t.Run("falls back to after when there is no head commit", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-GitHub-Event", "push")
		body := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/web"},"after":"fedcba9876543210"}`)

		ev, err := p.ParsePush(h, body, conn)
		if err != nil {
			t.Fatalf("ParsePush: %v", err)
		}
		if ev.CommitSHA != "fedcba9876543210" {
			t.Errorf("CommitSHA = %q, want the after field", ev.CommitSHA)
		}
	})

	t.Run("an unusable repository is an error, not a silent miss", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-GitHub-Event", "push")
		body := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"nonsense"}}`)

		if _, err := p.ParsePush(h, body, conn); err == nil {
			t.Error("a single-segment repository parsed without error")
		}
	})
}

func TestInstallURL(t *testing.T) {
	p := &GitHubProvider{}

	user := git.Connection{Metadata: map[string]string{"appSlug": "vesta-deploy"}}
	if got, want := p.InstallURL(user), "https://github.com/settings/apps/vesta-deploy/installations"; got != want {
		t.Errorf("InstallURL(user) = %q, want %q", got, want)
	}

	org := git.Connection{
		Account:  "acme",
		Metadata: map[string]string{"appSlug": "vesta-deploy", "ownerType": "Organization"},
	}
	if got, want := p.InstallURL(org),
		"https://github.com/organizations/acme/settings/apps/vesta-deploy/installations"; got != want {
		t.Errorf("InstallURL(org) = %q, want %q", got, want)
	}

	// No slug means no usable link, and the UI hides the button rather than offering a
	// broken one.
	if got := p.InstallURL(git.Connection{}); got != "" {
		t.Errorf("InstallURL(unconfigured) = %q, want empty", got)
	}
}

// The registry is what lets spec.git.provider finally mean something. A kind nothing
// implements must report itself missing rather than falling through to GitHub, which is
// exactly what used to happen: the field was written by the UI and read by nobody, so
// picking GitLab silently behaved as GitHub.
func TestRegistryDoesNotFallBackToGitHub(t *testing.T) {
	r := git.NewRegistry(NewGitHubProvider(nil, nil))

	if _, ok := r.Get(git.ProviderGitHub); !ok {
		t.Fatal("github is not registered")
	}
	// gitea is in the CRD enum and has no implementation, which is the case this guards.
	for _, kind := range []string{git.ProviderGitLab, git.ProviderBitbucket, "gitea", ""} {
		if p, ok := r.Get(kind); ok {
			t.Errorf("Get(%q) returned %T from a registry holding only GitHub", kind, p)
		}
	}
}

// Every provider the CRD enum accepts and Vesta claims to support must actually be wired.
// A missing one fails closed -- the webhook is recorded and ignored -- which looks exactly
// like a misconfigured hook.
func TestEveryClaimedProviderIsImplemented(t *testing.T) {
	r := git.NewRegistry(
		NewGitHubProvider(nil, nil),
		NewGitLabProvider(nil),
		NewBitbucketProvider(nil),
	)

	for _, kind := range []string{git.ProviderGitHub, git.ProviderGitLab, git.ProviderBitbucket} {
		p, ok := r.Get(kind)
		if !ok {
			t.Errorf("provider %q is not registered", kind)
			continue
		}
		if p.Kind() != kind {
			t.Errorf("provider registered under %q reports Kind() = %q", kind, p.Kind())
		}
	}
}
