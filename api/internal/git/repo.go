// Package git holds the provider-neutral vocabulary for talking about repositories.
//
// It exists because "which repository is this?" used to be answered by comparing two raw
// strings with !=. A push carried repository.full_name, an app carried whatever someone
// typed into a text box, and the webhook matched them literally. That failed silently in
// every direction: a pasted https://github.com/org/repo never matched org/repo, a trailing
// .git never matched, Acme/Web never matched acme/web even though GitHub treats them as the
// same repository, and there was no provider dimension at all -- so a GitLab push to
// acme/web would have matched a GitHub-backed app once GitLab existed.
//
// Silently is the important word. A mismatch produced no error anywhere; the app simply
// never deployed and nothing said why.
package git

import (
	"fmt"
	"strings"
)

// Provider kinds. These match the enum already shipped in the VestaApp CRD, so a stored
// spec.git.provider is always one of these.
const (
	ProviderGitHub    = "github"
	ProviderGitLab    = "gitlab"
	ProviderBitbucket = "bitbucket"
)

// defaultHosts maps a provider to its SaaS host. A connection to self-managed GitLab or
// Bitbucket Data Center carries its own host, which overrides this.
var defaultHosts = map[string]string{
	ProviderGitHub:    "github.com",
	ProviderGitLab:    "gitlab.com",
	ProviderBitbucket: "bitbucket.org",
}

// DefaultHost returns the SaaS host for a provider, or "" if the provider is unknown.
func DefaultHost(provider string) string {
	return defaultHosts[strings.ToLower(strings.TrimSpace(provider))]
}

// RepoRef identifies a repository across providers and hosts.
//
// Host is part of the identity, not decoration: once self-managed GitLab is in play, the
// same Path can exist on gitlab.com and on gitlab.internal, and they are different
// repositories.
type RepoRef struct {
	Provider string // github | gitlab | bitbucket
	Host     string // github.com, gitlab.example.com
	Path     string // acme/web, or group/subgroup/project on GitLab
}

// Key returns a stable comparable identity, useful as a map key and in logs.
func (r RepoRef) Key() string {
	return r.Provider + ":" + r.Host + ":" + r.Path
}

// Equal reports whether two refs name the same repository.
func (r RepoRef) Equal(o RepoRef) bool {
	return r.Provider == o.Provider && r.Host == o.Host && r.Path == o.Path
}

// String renders the ref the way a user would recognise it.
func (r RepoRef) String() string {
	if r.Host == "" {
		return r.Path
	}
	return r.Host + "/" + r.Path
}

// Owner returns the first path segment -- the GitHub owner, GitLab top-level group, or
// Bitbucket workspace.
//
// Callers that need owner and repository separately should use this with Name rather than
// splitting Path themselves: a GitLab project can sit under nested subgroups, so the
// repository is the LAST segment, not the second.
func (r RepoRef) Owner() string {
	if i := strings.Index(r.Path, "/"); i >= 0 {
		return r.Path[:i]
	}
	return r.Path
}

// Name returns the final path segment, the repository itself.
func (r RepoRef) Name() string {
	if i := strings.LastIndex(r.Path, "/"); i >= 0 {
		return r.Path[i+1:]
	}
	return r.Path
}

// ParseRepoRef normalises whatever form a repository was given in into a RepoRef.
//
// host is the connection's host and may be empty, in which case the provider's SaaS host is
// used. A host embedded in raw always wins -- if somebody pasted a full URL, that URL names
// the server they meant, and honouring the connection's host instead would silently point
// at the wrong one.
//
// Accepted forms:
//
//	acme/web
//	group/subgroup/project
//	https://github.com/acme/web        (with or without .git, with or without a trailing /)
//	http://gitlab.internal/group/proj
//	ssh://git@github.com/acme/web.git
//	git@github.com:acme/web.git
func ParseRepoRef(provider, host, raw string) (RepoRef, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return RepoRef{}, fmt.Errorf("repository %q has no provider", raw)
	}
	if _, known := defaultHosts[provider]; !known {
		return RepoRef{}, fmt.Errorf("unknown git provider %q", provider)
	}

	s := strings.TrimSpace(raw)
	if s == "" {
		return RepoRef{}, fmt.Errorf("repository is empty")
	}

	embeddedHost := ""

	// scp-style: git@host:path. Checked before the scheme split because it has no "//"
	// and would otherwise fall through to being treated as a bare path.
	if !strings.Contains(s, "://") {
		if at := strings.Index(s, "@"); at >= 0 {
			if colon := strings.Index(s[at:], ":"); colon >= 0 {
				embeddedHost = s[at+1 : at+colon]
				s = s[at+colon+1:]
			}
		}
	} else {
		// scheme://[user[:pass]@]host/path
		s = s[strings.Index(s, "://")+3:]
		if at := strings.LastIndex(s, "@"); at >= 0 {
			s = s[at+1:] // drop any embedded credentials; they are not identity
		}
		if slash := strings.Index(s, "/"); slash >= 0 {
			embeddedHost = s[:slash]
			s = s[slash+1:]
		} else {
			return RepoRef{}, fmt.Errorf("repository %q has a host but no path", raw)
		}
	}

	if embeddedHost != "" {
		host = embeddedHost
	}
	if host == "" {
		host = defaultHosts[provider]
	}

	path := strings.Trim(s, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")

	if path == "" {
		return RepoRef{}, fmt.Errorf("repository %q has no path", raw)
	}
	if !strings.Contains(path, "/") {
		return RepoRef{}, fmt.Errorf("repository %q needs at least an owner and a name, e.g. acme/web", raw)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			return RepoRef{}, fmt.Errorf("repository %q has an empty path segment", raw)
		}
	}

	// Lowercased because GitHub and Bitbucket treat names case-insensitively, so Acme/Web
	// and acme/web are one repository and must compare equal. GitLab paths are
	// case-sensitive in principle, but it forces lowercase on creation, so folding here
	// costs nothing real and buys one comparison rule instead of three.
	return RepoRef{
		Provider: provider,
		Host:     strings.ToLower(strings.Trim(host, "/")),
		Path:     strings.ToLower(path),
	}, nil
}
