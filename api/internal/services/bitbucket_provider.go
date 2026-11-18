package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// BitbucketProvider serves both Bitbucket Cloud and Bitbucket Data Center.
//
// They share a name and almost nothing else: different REST APIs, different webhook payload
// shapes, different build-status endpoints, and different webhook authentication. A
// connection with a BaseURL is Data Center; without one it is Cloud.
type BitbucketProvider struct {
	clientset kubernetes.Interface
	http      *http.Client
}

var _ git.Provider = (*BitbucketProvider)(nil)

func NewBitbucketProvider(clientset kubernetes.Interface) *BitbucketProvider {
	return &BitbucketProvider{
		clientset: clientset,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *BitbucketProvider) Kind() string { return git.ProviderBitbucket }

// isDataCenter reports whether a connection points at self-hosted Bitbucket.
func isDataCenter(conn git.Connection) bool {
	if conn.BaseURL != "" {
		return true
	}
	return conn.Host != "" && conn.Host != git.DefaultHost(git.ProviderBitbucket)
}

// VerifyWebhook authenticates a delivery.
//
// The two products differ in a way that matters for security. Data Center signs the body
// with HMAC-SHA256 in X-Hub-Signature, the same shape GitHub uses. Bitbucket Cloud signs
// nothing at all -- it offers no webhook secret -- so the only thing available is a secret
// carried in the URL, which the handler puts in the connection-scoped path. That is weaker
// than a signature and it is worth being honest about: it proves the caller knows a URL,
// not that the body is untampered.
func (p *BitbucketProvider) VerifyWebhook(h http.Header, body []byte, secret string) error {
	sig := h.Get("X-Hub-Signature")
	if sig == "" {
		// Cloud, which offers no webhook secret at all. There is nothing to verify, so
		// the handler's rule takes over: a provider that cannot sign is only accepted on
		// a connection-scoped URL, where the unguessable path is the whole credential.
		return git.ErrNoSignature
	}

	if secret == "" {
		return fmt.Errorf("no webhook secret configured")
	}

	// Data Center sends "sha256=<hex>".
	value := strings.TrimPrefix(sig, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(want), []byte(value)) != 1 {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

func (p *BitbucketProvider) ParsePush(h http.Header, body []byte, conn git.Connection) (*git.PushEvent, error) {
	event := h.Get("X-Event-Key")
	if event != "" && event != "repo:push" {
		return nil, git.ErrNotAPush
	}

	if isDataCenter(conn) {
		return p.parseDataCenterPush(body, conn)
	}
	return p.parseCloudPush(body, conn)
}

// parseCloudPush reads Bitbucket Cloud's shape.
//
// The branch arrives as a bare name, not a refs/heads/ ref, so it is turned into one here
// rather than leaving every consumer to wonder which form it has.
func (p *BitbucketProvider) parseCloudPush(body []byte, conn git.Connection) (*git.PushEvent, error) {
	var payload struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Actor struct {
			Nickname string `json:"nickname"`
		} `json:"actor"`
		Push struct {
			Changes []struct {
				New *struct {
					Name   string `json:"name"`
					Type   string `json:"type"`
					Target struct {
						Hash string `json:"hash"`
					} `json:"target"`
				} `json:"new"`
			} `json:"changes"`
		} `json:"push"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode push payload: %w", err)
	}

	var branch, sha string
	for _, ch := range payload.Push.Changes {
		// A deleted branch has a null "new"; a tag push has type "tag". Neither is a
		// branch push and neither should deploy anything.
		if ch.New == nil || ch.New.Type != "branch" {
			continue
		}
		branch = ch.New.Name
		sha = ch.New.Target.Hash
		break
	}
	if branch == "" {
		return nil, git.ErrNotAPush
	}

	ref, err := git.ParseRepoRef(git.ProviderBitbucket, conn.Host, payload.Repository.FullName)
	if err != nil {
		return nil, fmt.Errorf("push names an unusable repository: %w", err)
	}

	return &git.PushEvent{
		Repo:      ref,
		Ref:       "refs/heads/" + branch,
		Branch:    branch,
		CommitSHA: sha,
		Pusher:    payload.Actor.Nickname,
	}, nil
}

// parseDataCenterPush reads Bitbucket Server/Data Center's shape, which shares no field
// names with Cloud's. The repository is addressed by project key and slug rather than a
// full name.
func (p *BitbucketProvider) parseDataCenterPush(body []byte, conn git.Connection) (*git.PushEvent, error) {
	var payload struct {
		EventKey   string `json:"eventKey"`
		Repository struct {
			Slug    string `json:"slug"`
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
		} `json:"repository"`
		Actor struct {
			Name string `json:"name"`
		} `json:"actor"`
		Changes []struct {
			RefID  string `json:"refId"`
			ToHash string `json:"toHash"`
			Type   string `json:"type"`
			Ref    struct {
				Type string `json:"type"`
			} `json:"ref"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode push payload: %w", err)
	}

	var refID, sha string
	for _, ch := range payload.Changes {
		if ch.Type == "DELETE" || !strings.HasPrefix(ch.RefID, "refs/heads/") {
			continue
		}
		refID = ch.RefID
		sha = ch.ToHash
		break
	}
	if refID == "" {
		return nil, git.ErrNotAPush
	}

	path := payload.Repository.Project.Key + "/" + payload.Repository.Slug
	ref, err := git.ParseRepoRef(git.ProviderBitbucket, conn.Host, path)
	if err != nil {
		return nil, fmt.Errorf("push names an unusable repository: %w", err)
	}

	return &git.PushEvent{
		Repo:      ref,
		Ref:       refID,
		Branch:    branchFromRef(refID),
		CommitSHA: sha,
		Pusher:    payload.Actor.Name,
	}, nil
}

func (p *BitbucketProvider) CredentialFor(ctx context.Context, conn git.Connection, repo git.RepoRef) (git.Credential, error) {
	token, err := p.token(ctx, conn)
	if err != nil {
		return git.Credential{}, err
	}
	return bitbucketCredential(token), nil
}

// bitbucketCredential is how a Bitbucket token is used: Bearer for the API, and
// "x-token-auth" as the basic-auth username when cloning.
func bitbucketCredential(token string) git.Credential {
	if token == "" {
		return git.Credential{}
	}
	return git.Credential{Scheme: "Bearer", Username: "x-token-auth", Token: token}
}

func (p *BitbucketProvider) token(ctx context.Context, conn git.Connection) (string, error) {
	if conn.SecretName == "" {
		return "", fmt.Errorf("connection has no credential")
	}
	secret, err := p.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, conn.SecretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read bitbucket token: %w", err)
	}
	token := strings.TrimSpace(string(secret.Data["token"]))
	if token == "" {
		return "", fmt.Errorf("connection secret %s holds no token", conn.SecretName)
	}
	return token, nil
}

func (p *BitbucketProvider) apiBase(conn git.Connection) string {
	if isDataCenter(conn) {
		base := conn.BaseURL
		if base == "" {
			base = "https://" + conn.Host
		}
		return strings.TrimSuffix(base, "/") + "/rest/api/1.0"
	}
	return "https://api.bitbucket.org/2.0"
}

func (p *BitbucketProvider) ListRepos(ctx context.Context, conn git.Connection) ([]git.Repo, error) {
	if isDataCenter(conn) {
		var out struct {
			Values []struct {
				Slug    string `json:"slug"`
				Project struct {
					Key string `json:"key"`
				} `json:"project"`
				Public bool `json:"public"`
			} `json:"values"`
		}
		if err := p.get(ctx, conn, "/repos?limit=100", &out); err != nil {
			return nil, err
		}
		repos := make([]git.Repo, 0, len(out.Values))
		for _, r := range out.Values {
			ref, err := git.ParseRepoRef(git.ProviderBitbucket, conn.Host, r.Project.Key+"/"+r.Slug)
			if err != nil {
				continue
			}
			repos = append(repos, git.Repo{Ref: ref, Private: !r.Public, ConnectionID: conn.ID})
		}
		return repos, nil
	}

	if conn.Account == "" {
		return nil, fmt.Errorf("bitbucket needs a workspace to list repositories")
	}

	var out struct {
		Values []struct {
			FullName   string `json:"full_name"`
			IsPrivate  bool   `json:"is_private"`
			MainBranch struct {
				Name string `json:"name"`
			} `json:"mainbranch"`
		} `json:"values"`
	}
	if err := p.get(ctx, conn, "/repositories/"+url.PathEscape(conn.Account)+"?pagelen=100", &out); err != nil {
		return nil, err
	}

	repos := make([]git.Repo, 0, len(out.Values))
	for _, r := range out.Values {
		ref, err := git.ParseRepoRef(git.ProviderBitbucket, conn.Host, r.FullName)
		if err != nil {
			continue
		}
		repos = append(repos, git.Repo{
			Ref: ref, Private: r.IsPrivate, DefaultRef: r.MainBranch.Name, ConnectionID: conn.ID,
		})
	}
	return repos, nil
}

func (p *BitbucketProvider) ListBranches(ctx context.Context, conn git.Connection, repo git.RepoRef) ([]string, error) {
	if isDataCenter(conn) {
		var out struct {
			Values []struct {
				DisplayID string `json:"displayId"`
			} `json:"values"`
		}
		path := fmt.Sprintf("/projects/%s/repos/%s/branches?limit=100",
			url.PathEscape(repo.Owner()), url.PathEscape(repo.Name()))
		if err := p.get(ctx, conn, path, &out); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(out.Values))
		for _, b := range out.Values {
			names = append(names, b.DisplayID)
		}
		return names, nil
	}

	var out struct {
		Values []struct {
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := p.get(ctx, conn, "/repositories/"+repo.Path+"/refs/branches?pagelen=100", &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Values))
	for _, b := range out.Values {
		names = append(names, b.Name)
	}
	return names, nil
}

func (p *BitbucketProvider) ReportStatus(ctx context.Context, conn git.Connection, repo git.RepoRef,
	sha string, state git.BuildState, targetURL, description string) error {

	if sha == "" {
		return fmt.Errorf("no commit to report against")
	}

	// Bitbucket requires a key that identifies this check, and reuses it to update the
	// same status rather than adding another. Without a stable key every build would leave
	// a new entry on the commit.
	body := map[string]string{
		"key":         "vesta",
		"state":       bitbucketState(state),
		"name":        "Vesta",
		"description": description,
	}
	// The field is required on Cloud even when there is nothing useful to point at.
	if targetURL != "" {
		body["url"] = targetURL
	} else {
		body["url"] = "https://vesta.sh"
	}

	var path string
	if isDataCenter(conn) {
		path = "/projects/" + url.PathEscape(repo.Owner()) +
			"/repos/" + url.PathEscape(repo.Name()) +
			"/commits/" + url.PathEscape(sha) + "/builds"
	} else {
		path = "/repositories/" + repo.Path + "/commit/" + url.PathEscape(sha) + "/statuses/build"
	}
	return p.post(ctx, conn, path, body)
}

// bitbucketState maps the neutral vocabulary onto Bitbucket's, which is upper case and has
// no distinct "error" -- a failure and an error are both FAILED.
func bitbucketState(s git.BuildState) string {
	switch s {
	case git.StatePending, git.StateRunning:
		return "INPROGRESS"
	case git.StateSuccess:
		return "SUCCESSFUL"
	case git.StateFailed, git.StateError:
		return "FAILED"
	case git.StateCanceled:
		return "STOPPED"
	}
	return "FAILED"
}

// InstallURL points at where access tokens are managed. Like GitLab, Bitbucket grants access
// through a token's scope rather than by installing anything.
func (p *BitbucketProvider) InstallURL(conn git.Connection) string {
	if isDataCenter(conn) {
		base := conn.BaseURL
		if base == "" {
			base = "https://" + conn.Host
		}
		return strings.TrimSuffix(base, "/") + "/plugins/servlet/access-tokens/manage"
	}
	if conn.Account == "" {
		return ""
	}
	return "https://bitbucket.org/" + conn.Account + "/workspace/settings/access-tokens"
}

func (p *BitbucketProvider) get(ctx context.Context, conn git.Connection, path string, into interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase(conn)+path, nil)
	if err != nil {
		return err
	}
	if err := p.authorize(ctx, conn, req); err != nil {
		return err
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach bitbucket: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return bitbucketError(resp.StatusCode, body)
	}
	return json.Unmarshal(body, into)
}

func (p *BitbucketProvider) post(ctx context.Context, conn git.Connection, path string, payload interface{}) error {
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
		return fmt.Errorf("could not reach bitbucket: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return bitbucketError(resp.StatusCode, body)
	}
	return nil
}

func (p *BitbucketProvider) authorize(ctx context.Context, conn git.Connection, req *http.Request) error {
	cred, err := p.CredentialFor(ctx, conn, git.RepoRef{})
	if err != nil {
		return err
	}
	cred.Apply(req)
	return nil
}

func bitbucketError(status int, body []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("bitbucket rejected the token (HTTP %d)", status)
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("bitbucket returned 404 -- the repository does not exist, or this token cannot see it")
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("bitbucket returned HTTP %d: %s", status, msg)
}
