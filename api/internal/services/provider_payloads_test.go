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

// Push payloads share no field names across the three providers. GitHub has ref and
// head_commit.id, GitLab has object_kind and checkout_sha, Bitbucket Cloud has
// push.changes[].new.name with no ref at all. Getting one wrong means pushes to that
// provider silently never deploy, which is exactly what a stub used to do.

func TestGitLabParsePush(t *testing.T) {
	p := NewGitLabProvider(nil)
	conn := git.Connection{Provider: git.ProviderGitLab}

	t.Run("a push", func(t *testing.T) {
		body := []byte(`{
			"object_kind": "push",
			"ref": "refs/heads/main",
			"checkout_sha": "abc1234567890",
			"user_username": "alice",
			"project": {"path_with_namespace": "group/sub/project"}
		}`)

		ev, err := p.ParsePush(http.Header{}, body, conn)
		if err != nil {
			t.Fatalf("ParsePush: %v", err)
		}
		if ev.Branch != "main" {
			t.Errorf("Branch = %q, want main", ev.Branch)
		}
		if ev.CommitSHA != "abc1234567890" {
			t.Errorf("CommitSHA = %q", ev.CommitSHA)
		}
		// Subgroups must survive whole. Splitting on the first slash would call this
		// project "sub/project" under owner "group" and never match the app.
		if ev.Repo.Path != "group/sub/project" {
			t.Errorf("Repo.Path = %q, want every subgroup segment", ev.Repo.Path)
		}
		if ev.Repo.Host != "gitlab.com" {
			t.Errorf("Repo.Host = %q", ev.Repo.Host)
		}
	})

	t.Run("self-managed host comes from the connection", func(t *testing.T) {
		body := []byte(`{"object_kind":"push","ref":"refs/heads/main","checkout_sha":"a","project":{"path_with_namespace":"g/p"}}`)
		ev, err := p.ParsePush(http.Header{}, body, git.Connection{
			Provider: git.ProviderGitLab, Host: "gitlab.internal",
		})
		if err != nil {
			t.Fatalf("ParsePush: %v", err)
		}
		if ev.Repo.Host != "gitlab.internal" {
			t.Errorf("Repo.Host = %q, want the connection's host", ev.Repo.Host)
		}
	})

	t.Run("a tag push is not a branch push", func(t *testing.T) {
		body := []byte(`{"object_kind":"tag_push","ref":"refs/tags/v1","project":{"path_with_namespace":"g/p"}}`)
		if _, err := p.ParsePush(http.Header{}, body, conn); !errors.Is(err, git.ErrNotAPush) {
			t.Errorf("error = %v, want ErrNotAPush", err)
		}
	})

	t.Run("falls back to after", func(t *testing.T) {
		body := []byte(`{"object_kind":"push","ref":"refs/heads/main","after":"def456","project":{"path_with_namespace":"g/p"}}`)
		ev, err := p.ParsePush(http.Header{}, body, conn)
		if err != nil {
			t.Fatalf("ParsePush: %v", err)
		}
		if ev.CommitSHA != "def456" {
			t.Errorf("CommitSHA = %q, want the after field", ev.CommitSHA)
		}
	})
}

// GitLab echoes a shared token rather than signing the body, so the comparison is an
// equality test -- and must be constant time, because unlike an HMAC there is no digest
// hiding the secret from a timing measurement.
func TestGitLabVerifyWebhook(t *testing.T) {
	p := NewGitLabProvider(nil)

	header := func(token string) http.Header {
		h := http.Header{}
		if token != "" {
			h.Set("X-Gitlab-Token", token)
		}
		return h
	}

	if err := p.VerifyWebhook(header("s3cret"), nil, "s3cret"); err != nil {
		t.Errorf("a matching token was rejected: %v", err)
	}
	if err := p.VerifyWebhook(header("wrong"), nil, "s3cret"); err == nil {
		t.Error("a wrong token was accepted")
	}
	if err := p.VerifyWebhook(header(""), nil, "s3cret"); !errors.Is(err, git.ErrNoSignature) {
		t.Errorf("an absent token gave %v, want ErrNoSignature so the handler can apply policy", err)
	}
	if err := p.VerifyWebhook(header("anything"), nil, ""); err == nil {
		t.Error("a token was accepted with no secret configured to check it against")
	}
}

func TestBitbucketCloudParsePush(t *testing.T) {
	p := NewBitbucketProvider(nil)
	conn := git.Connection{Provider: git.ProviderBitbucket}

	t.Run("a push", func(t *testing.T) {
		body := []byte(`{
			"repository": {"full_name": "workspace/repo"},
			"actor": {"nickname": "alice"},
			"push": {"changes": [{"new": {"name": "main", "type": "branch", "target": {"hash": "abc123"}}}]}
		}`)

		h := http.Header{}
		h.Set("X-Event-Key", "repo:push")

		ev, err := p.ParsePush(h, body, conn)
		if err != nil {
			t.Fatalf("ParsePush: %v", err)
		}
		if ev.Branch != "main" {
			t.Errorf("Branch = %q", ev.Branch)
		}
		// Bitbucket sends a bare branch name. Everything downstream compares refs, so it
		// has to become one here rather than at each consumer.
		if ev.Ref != "refs/heads/main" {
			t.Errorf("Ref = %q, want a refs/heads/ form", ev.Ref)
		}
		if ev.CommitSHA != "abc123" {
			t.Errorf("CommitSHA = %q", ev.CommitSHA)
		}
	})

	t.Run("a deleted branch is not a push", func(t *testing.T) {
		body := []byte(`{"repository":{"full_name":"w/r"},"push":{"changes":[{"new":null}]}}`)
		if _, err := p.ParsePush(http.Header{}, body, conn); !errors.Is(err, git.ErrNotAPush) {
			t.Errorf("error = %v, want ErrNotAPush -- deleting a branch must not deploy", err)
		}
	})

	t.Run("a tag is not a branch", func(t *testing.T) {
		body := []byte(`{"repository":{"full_name":"w/r"},"push":{"changes":[{"new":{"name":"v1","type":"tag","target":{"hash":"a"}}}]}}`)
		if _, err := p.ParsePush(http.Header{}, body, conn); !errors.Is(err, git.ErrNotAPush) {
			t.Errorf("error = %v, want ErrNotAPush", err)
		}
	})

	t.Run("a non-push event key", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Event-Key", "pullrequest:created")
		if _, err := p.ParsePush(h, []byte(`{}`), conn); !errors.Is(err, git.ErrNotAPush) {
			t.Errorf("error = %v, want ErrNotAPush", err)
		}
	})
}

// Data Center's payload shares no field names with Cloud's, and the repository is addressed
// by project key and slug rather than a full name.
func TestBitbucketDataCenterParsePush(t *testing.T) {
	p := NewBitbucketProvider(nil)
	conn := git.Connection{Provider: git.ProviderBitbucket, Host: "bitbucket.internal", BaseURL: "https://bitbucket.internal"}

	body := []byte(`{
		"eventKey": "repo:refs_changed",
		"actor": {"name": "alice"},
		"repository": {"slug": "web", "project": {"key": "ACME"}},
		"changes": [{"refId": "refs/heads/main", "toHash": "abc123", "type": "UPDATE"}]
	}`)

	ev, err := p.ParsePush(http.Header{}, body, conn)
	if err != nil {
		t.Fatalf("ParsePush: %v", err)
	}
	if ev.Branch != "main" {
		t.Errorf("Branch = %q", ev.Branch)
	}
	if ev.Repo.Path != "acme/web" {
		t.Errorf("Repo.Path = %q, want the project key and slug", ev.Repo.Path)
	}
	if ev.Repo.Host != "bitbucket.internal" {
		t.Errorf("Repo.Host = %q", ev.Repo.Host)
	}
}

func TestBitbucketVerifyWebhook(t *testing.T) {
	p := NewBitbucketProvider(nil)
	body := []byte(`{"push":{}}`)

	sign := func(secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	h := http.Header{}
	h.Set("X-Hub-Signature", sign("s3cret"))
	if err := p.VerifyWebhook(h, body, "s3cret"); err != nil {
		t.Errorf("a valid Data Center signature was rejected: %v", err)
	}

	h.Set("X-Hub-Signature", sign("wrong"))
	if err := p.VerifyWebhook(h, body, "s3cret"); err == nil {
		t.Error("a wrong signature was accepted")
	}

	// Cloud sends nothing. The provider must say so rather than deciding for itself that
	// this is fine -- only the handler knows whether the URL was connection-scoped.
	if err := p.VerifyWebhook(http.Header{}, body, "s3cret"); !errors.Is(err, git.ErrNoSignature) {
		t.Errorf("an unsigned Cloud delivery gave %v, want ErrNoSignature", err)
	}
}

// Each provider spells the same outcomes differently, and sending one provider's word to
// another is rejected by its API -- leaving the check stuck on whatever it last was.
func TestStateVocabulariesAreDistinct(t *testing.T) {
	gitlabValid := map[string]bool{
		"pending": true, "running": true, "success": true, "failed": true, "canceled": true,
	}
	bitbucketValid := map[string]bool{
		"INPROGRESS": true, "SUCCESSFUL": true, "FAILED": true, "STOPPED": true,
	}

	all := []git.BuildState{
		git.StatePending, git.StateRunning, git.StateSuccess,
		git.StateFailed, git.StateError, git.StateCanceled,
	}

	for _, s := range all {
		if got := gitlabState(s); !gitlabValid[got] {
			t.Errorf("gitlabState(%v) = %q, which GitLab does not accept", s, got)
		}
		if got := bitbucketState(s); !bitbucketValid[got] {
			t.Errorf("bitbucketState(%v) = %q, which Bitbucket does not accept", s, got)
		}
	}

	// The specific spellings that differ and are easy to get wrong.
	if gitlabState(git.StateFailed) != "failed" {
		t.Errorf(`gitlabState(failed) = %q, want "failed" -- GitHub's "failure" is rejected`,
			gitlabState(git.StateFailed))
	}
	if bitbucketState(git.StateSuccess) != "SUCCESSFUL" {
		t.Errorf(`bitbucketState(success) = %q, want "SUCCESSFUL"`, bitbucketState(git.StateSuccess))
	}
	// A finished build must never still read as in progress.
	for _, s := range []git.BuildState{git.StateSuccess, git.StateFailed} {
		if bitbucketState(s) == "INPROGRESS" || gitlabState(s) == "running" {
			t.Errorf("finished state %v still maps to an in-progress value", s)
		}
	}
}

// The clone username differs per provider and the wrong one fails with a bare 403, which
// reads like a permissions problem rather than a wrong username.
func TestCloneUsernamesDiffer(t *testing.T) {
	cases := map[string]git.Credential{
		"github":    githubCredential("t"),
		"gitlab":    gitlabCredential("t"),
		"bitbucket": bitbucketCredential("t"),
	}
	want := map[string]string{
		"github": "x-access-token", "gitlab": "oauth2", "bitbucket": "x-token-auth",
	}

	for provider, cred := range cases {
		if cred.Username != want[provider] {
			t.Errorf("%s clone username = %q, want %q", provider, cred.Username, want[provider])
		}
		if cred.Empty() {
			t.Errorf("%s credential with a token reported itself empty", provider)
		}
	}

	// GitLab's API authentication is not an Authorization scheme at all.
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	gitlabCredential("tok").Apply(req)
	if req.Header.Get("PRIVATE-TOKEN") != "tok" {
		t.Error("GitLab credential did not set PRIVATE-TOKEN")
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("GitLab credential also set Authorization, which GitLab does not read")
	}
}
