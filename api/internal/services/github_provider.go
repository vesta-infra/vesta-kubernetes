package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kubernetes.getvesta.sh/api/internal/git"
)

// GitHubProvider adapts the existing GitHub App and status clients to git.Provider.
//
// It is deliberately thin. The GitHub-specific knowledge that used to be scattered --
// the Bearer scheme in the status notifier, the x-access-token clone username in the
// builder, the HMAC header name in the webhook handler -- is collected here, so a second
// provider is a new file rather than a search for every place GitHub was assumed.
type GitHubProvider struct {
	app    *GitHubAppService
	status *GitHubStatusNotifier
}

// Checked at compile time so a change to the interface fails here rather than at the call
// site that assembles the registry.
var _ git.Provider = (*GitHubProvider)(nil)

func NewGitHubProvider(app *GitHubAppService, status *GitHubStatusNotifier) *GitHubProvider {
	return &GitHubProvider{app: app, status: status}
}

func (p *GitHubProvider) Kind() string { return git.ProviderGitHub }

// VerifyWebhook checks the HMAC-SHA256 signature GitHub sends in X-Hub-Signature-256.
//
// A missing signature is an error here, not a pass. The handler decides whether to tolerate
// that during an upgrade; a provider must never be the thing that says an unsigned delivery
// is fine.
func (p *GitHubProvider) VerifyWebhook(h http.Header, body []byte, secret string) error {
	sig := h.Get("X-Hub-Signature-256")
	if sig == "" {
		return git.ErrNoSignature
	}
	if secret == "" {
		return fmt.Errorf("no webhook secret configured")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func (p *GitHubProvider) ParsePush(h http.Header, body []byte, conn git.Connection) (*git.PushEvent, error) {
	if event := h.Get("X-GitHub-Event"); event != "push" {
		return nil, git.ErrNotAPush
	}

	var payload struct {
		Ref        string `json:"ref"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		HeadCommit struct {
			ID string `json:"id"`
		} `json:"head_commit"`
		After  string `json:"after"`
		Pusher struct {
			Name string `json:"name"`
		} `json:"pusher"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode push payload: %w", err)
	}

	ref, err := git.ParseRepoRef(git.ProviderGitHub, conn.Host, payload.Repository.FullName)
	if err != nil {
		return nil, fmt.Errorf("push names an unusable repository: %w", err)
	}

	sha := payload.HeadCommit.ID
	if sha == "" {
		// A branch deletion, or a push GitHub summarised without a head commit.
		sha = payload.After
	}

	return &git.PushEvent{
		Repo:      ref,
		Ref:       payload.Ref,
		Branch:    branchFromRef(payload.Ref),
		CommitSHA: sha,
		Pusher:    payload.Pusher.Name,
	}, nil
}

func (p *GitHubProvider) CredentialFor(ctx context.Context, conn git.Connection, repo git.RepoRef) (git.Credential, error) {
	if p.app == nil {
		return git.Credential{}, fmt.Errorf("no GitHub App configured")
	}
	// Via the connection's own App. Minting with the wrong one produces a token that is
	// valid but has no access to this repository, and GitHub answers 404 -- which reads as
	// "no such repository" rather than "wrong credential" and sends you looking in the
	// wrong place.
	token, err := p.app.GetTokenForRepoVia(ctx, conn.SecretName, repo.Path)
	if err != nil {
		return git.Credential{}, err
	}
	return githubCredential(token), nil
}

// githubCredential is the shape a GitHub installation token has to be used in. Bearer for
// the REST API, and x-access-token as the basic-auth username when cloning -- the two facts
// the status notifier and the builder each used to hardcode separately.
func githubCredential(token string) git.Credential {
	if token == "" {
		return git.Credential{}
	}
	return git.Credential{Scheme: "Bearer", Username: "x-access-token", Token: token}
}

func (p *GitHubProvider) ListRepos(ctx context.Context, conn git.Connection) ([]git.Repo, error) {
	if p.app == nil || !p.app.IsConfigured() {
		return nil, fmt.Errorf("no GitHub App configured")
	}
	raw, err := p.app.ListAccessibleRepos(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]git.Repo, 0, len(raw))
	for _, r := range raw {
		ref, err := git.ParseRepoRef(git.ProviderGitHub, conn.Host, r.FullName)
		if err != nil {
			continue
		}
		out = append(out, git.Repo{Ref: ref, Private: r.Private, ConnectionID: conn.ID})
	}
	return out, nil
}

func (p *GitHubProvider) ListBranches(ctx context.Context, conn git.Connection, repo git.RepoRef) ([]string, error) {
	if p.app == nil || !p.app.IsConfigured() {
		return nil, fmt.Errorf("no GitHub App configured")
	}
	return p.app.ListRepoBranches(ctx, repo.Path)
}

func (p *GitHubProvider) ReportStatus(ctx context.Context, conn git.Connection, repo git.RepoRef,
	sha string, state git.BuildState, targetURL, description string) error {

	if p.status == nil {
		return fmt.Errorf("no status notifier")
	}
	if sha == "" {
		return fmt.Errorf("no commit to report against")
	}

	cred, err := p.CredentialFor(ctx, conn, repo)
	if err != nil {
		return err
	}
	if cred.Empty() {
		return fmt.Errorf("no credential for %s", repo)
	}

	return p.status.CreateCommitStatus(ctx, cred.Token, repo.Path, sha,
		githubState(state), targetURL, description, "vesta")
}

// githubState maps the neutral vocabulary onto GitHub's. GitHub has no "running", so a
// running build stays pending, and no "canceled", which reads closest to error.
func githubState(s git.BuildState) string {
	switch s {
	case git.StatePending, git.StateRunning:
		return "pending"
	case git.StateSuccess:
		return "success"
	case git.StateFailed:
		return "failure"
	case git.StateCanceled, git.StateError:
		return "error"
	}
	return "error"
}

// InstallURL points at the App's installation settings, where repositories are granted.
//
// The same two URL shapes were already being assembled in the settings page; they belong
// here so the app form and the settings page cannot drift apart.
func (p *GitHubProvider) InstallURL(conn git.Connection) string {
	slug := conn.Metadata["appSlug"]
	if slug == "" {
		return ""
	}
	if conn.Metadata["ownerType"] == "Organization" && conn.Account != "" {
		return fmt.Sprintf("https://github.com/organizations/%s/settings/apps/%s/installations",
			conn.Account, slug)
	}
	return fmt.Sprintf("https://github.com/settings/apps/%s/installations", slug)
}

// branchFromRef turns refs/heads/main into main, and reports "" for anything that is not a
// branch.
//
// The old code did ref[11:] behind a len(ref) > 11 check, which turned refs/tags/v1 into
// "v1" and let a tag push match a branch-triggered environment.
func branchFromRef(ref string) string {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimPrefix(ref, prefix)
}
