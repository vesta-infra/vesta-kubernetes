package services

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"kubernetes.getvesta.sh/api/internal/git"
)

// GitLabProvider talks to GitLab, SaaS or self-managed.
//
// Self-managed is why every URL is built from the connection's base rather than a constant:
// the same code serves gitlab.com and gitlab.internal, and the connection says which.
type GitLabProvider struct {
	clientset kubernetes.Interface
	http      *http.Client
}

var _ git.Provider = (*GitLabProvider)(nil)

func NewGitLabProvider(clientset kubernetes.Interface) *GitLabProvider {
	return &GitLabProvider{
		clientset: clientset,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *GitLabProvider) Kind() string { return git.ProviderGitLab }

// apiBase returns the /api/v4 root for a connection.
func (p *GitLabProvider) apiBase(conn git.Connection) string {
	base := conn.BaseURL
	if base == "" {
		host := conn.Host
		if host == "" {
			host = git.DefaultHost(git.ProviderGitLab)
		}
		base = "https://" + host
	}
	return strings.TrimSuffix(base, "/") + "/api/v4"
}

// VerifyWebhook checks the shared token GitLab sends.
//
// GitLab does not sign the body: it echoes back a secret the user typed when creating the
// hook. That makes this a plain equality test, and the comparison has to be constant time --
// a byte-by-byte early return on a token check is a timing oracle, and unlike an HMAC there
// is no digest to hide behind.
func (p *GitLabProvider) VerifyWebhook(h http.Header, body []byte, secret string) error {
	token := h.Get("X-Gitlab-Token")
	if token == "" {
		return git.ErrNoSignature
	}
	if secret == "" {
		return fmt.Errorf("no webhook secret configured")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return fmt.Errorf("invalid webhook token")
	}
	return nil
}

func (p *GitLabProvider) ParsePush(h http.Header, body []byte, conn git.Connection) (*git.PushEvent, error) {
	// GitLab names the event in a header and again in the body; the body is authoritative
	// because the header carries a display name ("Push Hook") that has changed before.
	var payload struct {
		ObjectKind  string `json:"object_kind"`
		Ref         string `json:"ref"`
		CheckoutSHA string `json:"checkout_sha"`
		After       string `json:"after"`
		UserName    string `json:"user_username"`
		Project     struct {
			PathWithNamespace string `json:"path_with_namespace"`
			WebURL            string `json:"web_url"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode push payload: %w", err)
	}
	if payload.ObjectKind != "push" {
		return nil, git.ErrNotAPush
	}

	// path_with_namespace keeps every subgroup segment, which is the whole reason repo
	// identity does not split on the first slash.
	ref, err := git.ParseRepoRef(git.ProviderGitLab, conn.Host, payload.Project.PathWithNamespace)
	if err != nil {
		return nil, fmt.Errorf("push names an unusable project: %w", err)
	}

	sha := payload.CheckoutSHA
	if sha == "" {
		sha = payload.After
	}

	return &git.PushEvent{
		Repo:      ref,
		Ref:       payload.Ref,
		Branch:    branchFromRef(payload.Ref),
		CommitSHA: sha,
		Pusher:    payload.UserName,
	}, nil
}

// CredentialFor returns the connection's token.
//
// GitLab has no per-repository credential: a personal or group access token grants what it
// grants, so unlike a GitHub App there is nothing to mint per repository.
func (p *GitLabProvider) CredentialFor(ctx context.Context, conn git.Connection, repo git.RepoRef) (git.Credential, error) {
	token, err := p.token(ctx, conn)
	if err != nil {
		return git.Credential{}, err
	}
	return gitlabCredential(token), nil
}

// gitlabCredential is how a GitLab token has to be used: PRIVATE-TOKEN for the API, and
// "oauth2" as the basic-auth username when cloning over HTTPS.
func gitlabCredential(token string) git.Credential {
	if token == "" {
		return git.Credential{}
	}
	return git.Credential{Scheme: "PRIVATE-TOKEN", Username: "oauth2", Token: token}
}

func (p *GitLabProvider) token(ctx context.Context, conn git.Connection) (string, error) {
	if conn.SecretName == "" {
		return "", fmt.Errorf("connection has no credential")
	}
	secret, err := p.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, conn.SecretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read gitlab token: %w", err)
	}
	token := strings.TrimSpace(string(secret.Data["token"]))
	if token == "" {
		return "", fmt.Errorf("connection secret %s holds no token", conn.SecretName)
	}
	return token, nil
}

func (p *GitLabProvider) ListRepos(ctx context.Context, conn git.Connection) ([]git.Repo, error) {
	var projects []struct {
		PathWithNamespace string `json:"path_with_namespace"`
		Visibility        string `json:"visibility"`
		DefaultBranch     string `json:"default_branch"`
	}
	// membership=true is what limits this to projects the token's owner actually belongs
	// to; without it GitLab returns every public project on the instance.
	if err := p.get(ctx, conn, "/projects?membership=true&per_page=100&order_by=last_activity_at", &projects); err != nil {
		return nil, err
	}

	out := make([]git.Repo, 0, len(projects))
	for _, pr := range projects {
		ref, err := git.ParseRepoRef(git.ProviderGitLab, conn.Host, pr.PathWithNamespace)
		if err != nil {
			continue
		}
		out = append(out, git.Repo{
			Ref:          ref,
			Private:      pr.Visibility != "public",
			DefaultRef:   pr.DefaultBranch,
			ConnectionID: conn.ID,
		})
	}
	return out, nil
}

func (p *GitLabProvider) ListBranches(ctx context.Context, conn git.Connection, repo git.RepoRef) ([]string, error) {
	var branches []struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/projects/%s/repository/branches?per_page=100", gitlabProjectID(repo))
	if err := p.get(ctx, conn, path, &branches); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(branches))
	for _, b := range branches {
		out = append(out, b.Name)
	}
	return out, nil
}

func (p *GitLabProvider) ReportStatus(ctx context.Context, conn git.Connection, repo git.RepoRef,
	sha string, state git.BuildState, targetURL, description string) error {

	if sha == "" {
		return fmt.Errorf("no commit to report against")
	}

	body := map[string]string{
		"state":       gitlabState(state),
		"name":        "vesta",
		"description": description,
	}
	if targetURL != "" {
		body["target_url"] = targetURL
	}

	path := fmt.Sprintf("/projects/%s/statuses/%s", gitlabProjectID(repo), url.PathEscape(sha))
	return p.post(ctx, conn, path, body)
}

// gitlabProjectID renders a project path the way GitLab's API wants it in a URL segment:
// fully escaped, so the slashes between subgroups do not become path separators.
func gitlabProjectID(repo git.RepoRef) string {
	return url.PathEscape(repo.Path)
}

// gitlabState maps the neutral vocabulary onto GitLab's.
//
// GitLab spells failure "failed" rather than "failure", has a "running" state GitHub does
// not, and accepts "canceled" -- so this is not the same table as GitHub's, which is why
// the vocabulary is neutral in the first place.
func gitlabState(s git.BuildState) string {
	switch s {
	case git.StatePending:
		return "pending"
	case git.StateRunning:
		return "running"
	case git.StateSuccess:
		return "success"
	case git.StateFailed, git.StateError:
		return "failed"
	case git.StateCanceled:
		return "canceled"
	}
	return "failed"
}

// InstallURL points at the token settings for this server.
//
// GitLab has no installation concept: access follows the token's scope, so "give Vesta more
// repositories" means widening or replacing the token rather than granting a repository.
func (p *GitLabProvider) InstallURL(conn git.Connection) string {
	base := conn.BaseURL
	if base == "" {
		host := conn.Host
		if host == "" {
			host = git.DefaultHost(git.ProviderGitLab)
		}
		base = "https://" + host
	}
	if conn.Account != "" {
		return fmt.Sprintf("%s/groups/%s/-/settings/access_tokens",
			strings.TrimSuffix(base, "/"), conn.Account)
	}
	return strings.TrimSuffix(base, "/") + "/-/user_settings/personal_access_tokens"
}

func (p *GitLabProvider) get(ctx context.Context, conn git.Connection, path string, into interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase(conn)+path, nil)
	if err != nil {
		return err
	}
	if err := p.authorize(ctx, conn, req); err != nil {
		return err
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach gitlab: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return gitlabError(resp.StatusCode, body)
	}
	return json.Unmarshal(body, into)
}

func (p *GitLabProvider) post(ctx context.Context, conn git.Connection, path string, payload interface{}) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiBase(conn)+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := p.authorize(ctx, conn, req); err != nil {
		return err
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach gitlab: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return gitlabError(resp.StatusCode, body)
	}
	return nil
}

func (p *GitLabProvider) authorize(ctx context.Context, conn git.Connection, req *http.Request) error {
	cred, err := p.CredentialFor(ctx, conn, git.RepoRef{})
	if err != nil {
		return err
	}
	cred.Apply(req)
	return nil
}

func gitlabError(status int, body []byte) error {
	// GitLab answers 404 for a project the token cannot see, which reads as "no such
	// project" and sends people looking for a typo rather than at the token's scope.
	if status == http.StatusNotFound {
		return fmt.Errorf("gitlab returned 404 -- the project does not exist, or this token cannot see it")
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("gitlab rejected the token (HTTP %d)", status)
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("gitlab returned HTTP %d: %s", status, msg)
}
