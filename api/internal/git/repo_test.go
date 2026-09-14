package git

import "testing"

func TestParseRepoRef(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		host     string
		raw      string
		want     RepoRef
	}{
		{
			"bare owner/repo",
			"github", "", "acme/web",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			// The form a user is most likely to paste, and the one that silently matched
			// nothing before: the webhook compared it to "acme/web" with != and moved on.
			"https url",
			"github", "", "https://github.com/acme/web",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			"https url with .git",
			"github", "", "https://github.com/acme/web.git",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			"https url with trailing slash",
			"github", "", "https://github.com/acme/web/",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			"scp-style ssh",
			"github", "", "git@github.com:acme/web.git",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			"ssh url",
			"github", "", "ssh://git@github.com/acme/web.git",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			// GitHub treats these as one repository, so they must compare equal. The old
			// literal != said otherwise and the app never deployed.
			"case is folded",
			"github", "", "Acme/Web",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			// splitRepo in github_app.go splits on the FIRST slash, so it reads this as
			// owner "group", repo "sub/project". Identity must keep the whole path.
			"gitlab subgroup keeps every segment",
			"gitlab", "", "group/sub/project",
			RepoRef{"gitlab", "gitlab.com", "group/sub/project"},
		},
		{
			"gitlab deep subgroup",
			"gitlab", "", "a/b/c/d/e",
			RepoRef{"gitlab", "gitlab.com", "a/b/c/d/e"},
		},
		{
			"self-managed host from the connection",
			"gitlab", "gitlab.internal", "group/proj",
			RepoRef{"gitlab", "gitlab.internal", "group/proj"},
		},
		{
			// A pasted URL names the server the user meant. Honouring the connection's
			// host instead would point at a different server with the same path.
			"host embedded in the url wins over the connection host",
			"gitlab", "gitlab.internal", "https://gitlab.example.com/group/proj",
			RepoRef{"gitlab", "gitlab.example.com", "group/proj"},
		},
		{
			"host with a port survives",
			"gitlab", "", "https://gitlab.internal:8443/group/proj",
			RepoRef{"gitlab", "gitlab.internal:8443", "group/proj"},
		},
		{
			// Credentials in a URL are not identity, and keeping them would put a token
			// into a comparison key and into logs.
			"embedded credentials are dropped",
			"github", "", "https://user:token@github.com/acme/web.git",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			"bitbucket defaults",
			"bitbucket", "", "workspace/repo",
			RepoRef{"bitbucket", "bitbucket.org", "workspace/repo"},
		},
		{
			"surrounding whitespace",
			"github", "", "  acme/web  ",
			RepoRef{"github", "github.com", "acme/web"},
		},
		{
			"provider case is folded",
			"GitHub", "", "acme/web",
			RepoRef{"github", "github.com", "acme/web"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRepoRef(tc.provider, tc.host, tc.raw)
			if err != nil {
				t.Fatalf("ParseRepoRef(%q, %q, %q) errored: %v", tc.provider, tc.host, tc.raw, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseRepoRef(%q, %q, %q) = %+v, want %+v",
					tc.provider, tc.host, tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseRepoRefRejects(t *testing.T) {
	cases := []struct{ name, provider, host, raw string }{
		{"empty repository", "github", "", ""},
		{"whitespace only", "github", "", "   "},
		{"no provider", "", "", "acme/web"},
		{"unknown provider", "svn", "", "acme/web"},
		// The create form seeds the field with a single-segment placeholder and this is
		// the shape a half-filled form produces.
		{"single segment", "github", "", "web"},
		{"url with no path", "github", "", "https://github.com"},
		{"empty path segment", "github", "", "acme//web"},
		{"only a slash", "github", "", "/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseRepoRef(tc.provider, tc.host, tc.raw); err == nil {
				t.Errorf("ParseRepoRef(%q, %q, %q) = %+v, want an error",
					tc.provider, tc.host, tc.raw, got)
			}
		})
	}
}

// The whole point of the type: forms that name one repository must compare equal, and
// repositories that merely look alike must not.
func TestRepoRefEquality(t *testing.T) {
	same := []struct{ a, b string }{
		{"acme/web", "https://github.com/acme/web"},
		{"acme/web", "git@github.com:acme/web.git"},
		{"acme/web", "Acme/Web"},
		{"acme/web", "https://github.com/acme/web.git/"},
	}
	for _, p := range same {
		a, err := ParseRepoRef("github", "", p.a)
		if err != nil {
			t.Fatalf("parse %q: %v", p.a, err)
		}
		b, err := ParseRepoRef("github", "", p.b)
		if err != nil {
			t.Fatalf("parse %q: %v", p.b, err)
		}
		if !a.Equal(b) {
			t.Errorf("%q and %q name the same repository but did not compare equal (%s vs %s)",
				p.a, p.b, a.Key(), b.Key())
		}
	}

	// Same path, different provider. Without the provider dimension a GitLab push would
	// trigger a deploy of a GitHub-backed app.
	gh, _ := ParseRepoRef("github", "", "acme/web")
	gl, _ := ParseRepoRef("gitlab", "", "acme/web")
	if gh.Equal(gl) {
		t.Error("acme/web on github and on gitlab compared equal; provider must be part of identity")
	}

	// Same path and provider, different server.
	saas, _ := ParseRepoRef("gitlab", "", "group/proj")
	self, _ := ParseRepoRef("gitlab", "gitlab.internal", "group/proj")
	if saas.Equal(self) {
		t.Error("group/proj on gitlab.com and on gitlab.internal compared equal; host must be part of identity")
	}
}

// Owner/Name exist so callers stop splitting Path by hand. A GitLab project under nested
// subgroups is the LAST segment; splitting on the first slash names a subgroup instead.
func TestOwnerAndName(t *testing.T) {
	cases := []struct {
		raw, provider, wantOwner, wantName string
	}{
		{"acme/web", "github", "acme", "web"},
		{"group/sub/project", "gitlab", "group", "project"},
		{"a/b/c/d/e", "gitlab", "a", "e"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r, err := ParseRepoRef(tc.provider, "", tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if r.Owner() != tc.wantOwner {
				t.Errorf("Owner() = %q, want %q", r.Owner(), tc.wantOwner)
			}
			if r.Name() != tc.wantName {
				t.Errorf("Name() = %q, want %q", r.Name(), tc.wantName)
			}
		})
	}
}

func TestDefaultHost(t *testing.T) {
	for provider, want := range map[string]string{
		"github":    "github.com",
		"gitlab":    "gitlab.com",
		"bitbucket": "bitbucket.org",
		"GitHub":    "github.com",
		"nonsense":  "",
	} {
		if got := DefaultHost(provider); got != want {
			t.Errorf("DefaultHost(%q) = %q, want %q", provider, got, want)
		}
	}
}
