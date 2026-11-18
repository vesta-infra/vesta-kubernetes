package git

import (
	"context"
	"fmt"
	"net/http"
)

// Credential is how a provider authenticates, not just what it authenticates with.
//
// This exists because the old getGitToken returned a bare string. Every consumer then had
// to guess how to use it, and each guessed GitHub: the status notifier sent
// "Authorization: Bearer <token>", the builder used it as an HTTP basic password with the
// username "x-access-token". Both are GitHub-App specific -- GitLab wants a PRIVATE-TOKEN
// header or the basic username "oauth2", Bitbucket wants "x-token-auth" -- so the knowledge
// has to travel with the token instead of being reinvented at each call site.
type Credential struct {
	// Scheme is the HTTP Authorization scheme: "Bearer", "token", "Basic", or
	// "PRIVATE-TOKEN" for GitLab's non-standard header. Empty means no authentication.
	Scheme string
	// Username is the basic-auth username used when cloning over HTTPS. Providers
	// disagree: GitHub Apps use "x-access-token", GitLab "oauth2", Bitbucket
	// "x-token-auth".
	Username string
	Token    string
}

// DefaultCloneUsername is the HTTPS basic-auth username a provider expects alongside a bare
// token, for the case where a user supplied a token themselves and Vesta has no credential
// object to carry it. They disagree, and the wrong one fails a clone with a bare 403.
func DefaultCloneUsername(provider string) string {
	switch provider {
	case ProviderGitLab:
		return "oauth2"
	case ProviderBitbucket:
		return "x-token-auth"
	default:
		return "x-access-token"
	}
}

// Empty reports whether there is nothing to authenticate with.
func (c Credential) Empty() bool { return c.Token == "" }

// Header renders the Authorization header value, or "" when there is nothing to send.
func (c Credential) Header() string {
	if c.Token == "" || c.Scheme == "" {
		return ""
	}
	return c.Scheme + " " + c.Token
}

// Apply sets the right authentication header on a request. GitLab's PRIVATE-TOKEN is not
// an Authorization scheme at all, which is exactly the sort of detail that should live here
// once rather than at every call site.
func (c Credential) Apply(r *http.Request) {
	if c.Token == "" {
		return
	}
	if c.Scheme == "PRIVATE-TOKEN" {
		r.Header.Set("PRIVATE-TOKEN", c.Token)
		return
	}
	if h := c.Header(); h != "" {
		r.Header.Set("Authorization", h)
	}
}

// BuildState is the outcome a provider is told about, in provider-neutral terms.
//
// The vocabularies genuinely differ and cannot be papered over with one string: GitHub uses
// pending/success/failure/error, GitLab uses pending/running/success/failed/canceled (note
// "failed", not "failure", and a "running" GitHub has no equivalent for), and Bitbucket
// uses INPROGRESS/SUCCESSFUL/FAILED/STOPPED in upper case with a required unique key.
// Mapping happens in each implementation.
type BuildState int

const (
	StatePending BuildState = iota
	StateRunning
	StateSuccess
	StateFailed
	StateError
	StateCanceled
)

func (s BuildState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateRunning:
		return "running"
	case StateSuccess:
		return "success"
	case StateFailed:
		return "failed"
	case StateError:
		return "error"
	case StateCanceled:
		return "canceled"
	}
	return "unknown"
}

// PushEvent is what every provider's push payload reduces to. The matching loop needs
// nothing else, which is what lets one implementation serve all three.
type PushEvent struct {
	Repo      RepoRef
	Ref       string // refs/heads/main
	Branch    string // main
	CommitSHA string
	Pusher    string
}

// Repo is one repository a connection can see.
type Repo struct {
	Ref          RepoRef
	Private      bool
	DefaultRef   string
	ConnectionID string
}

// Connection is one configured link to a git server.
//
// Credentials are deliberately not in here: they live in a Kubernetes Secret named by
// SecretName, so a connection can be listed, logged and returned to the UI without a token
// travelling with it.
type Connection struct {
	ID          string
	Provider    string
	DisplayName string
	BaseURL     string // empty means the provider's SaaS API
	Host        string
	Account     string
	SecretName  string
	ExternalID  string
	Metadata    map[string]string
}

// APIBase returns the API root for this connection.
func (c Connection) APIBase(saasDefault string) string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return saasDefault
}

// Provider is everything Vesta needs from a git host.
//
// Before this existed there was no abstraction at all -- api/internal had no interfaces
// whatsoever, and GitHub specifics were spread across the webhook handler, the builder and
// the status notifier. spec.git.provider was written by the UI and read by nothing, so
// selecting GitLab silently behaved as GitHub.
type Provider interface {
	// Kind is the value stored in spec.git.provider.
	Kind() string

	// VerifyWebhook reports whether a delivery is authentic. Schemes differ: GitHub signs
	// with HMAC-SHA256, GitLab sends a plain shared token, Bitbucket Cloud sends nothing.
	VerifyWebhook(h http.Header, body []byte, secret string) error

	// ParsePush turns a push payload into the neutral form, or reports that this delivery
	// is not a push.
	ParsePush(h http.Header, body []byte, conn Connection) (*PushEvent, error)

	// CredentialFor mints or fetches a credential for one repository.
	CredentialFor(ctx context.Context, conn Connection, repo RepoRef) (Credential, error)

	ListRepos(ctx context.Context, conn Connection) ([]Repo, error)
	ListBranches(ctx context.Context, conn Connection, repo RepoRef) ([]string, error)

	// ReportStatus posts a build outcome against a commit.
	ReportStatus(ctx context.Context, conn Connection, repo RepoRef, sha string, state BuildState, targetURL, description string) error

	// InstallURL is where a user goes to grant access to more repositories, or "" when the
	// provider has no such concept. GitHub has App installation settings; GitLab and
	// Bitbucket have token scopes instead.
	InstallURL(conn Connection) string
}

// ErrNotAPush is returned by ParsePush for a delivery that is a valid webhook but not a
// push -- a pull request, a tag, a ping. It is not a failure.
var ErrNotAPush = fmt.Errorf("not a push event")

// ErrNoSignature is returned by VerifyWebhook when the delivery carries no authentication
// at all.
//
// It is distinct from an invalid one because the two are answered differently: an invalid
// signature is always refused, while a missing one is refused only once the instance stops
// tolerating unsigned deliveries. A provider must never make that policy decision itself --
// it does not know whether this install is mid-upgrade.
var ErrNoSignature = fmt.Errorf("no signature")

// Registry maps a provider kind to its implementation.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry(ps ...Provider) *Registry {
	r := &Registry{providers: make(map[string]Provider, len(ps))}
	for _, p := range ps {
		r.providers[p.Kind()] = p
	}
	return r
}

// Get returns the implementation for a kind. The bool is false for a provider that is not
// built, which is how the CRD enum can list gitea before anything implements it.
func (r *Registry) Get(kind string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[kind]
	return p, ok
}

// Kinds lists the implemented providers.
func (r *Registry) Kinds() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	return out
}
