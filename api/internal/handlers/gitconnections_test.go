package handlers

import "testing"

// The host and the API base must agree, which is why only one is asked for. Two fields that
// have to match are two fields that can disagree -- and a connection whose host does not
// match its base URL never matches any push, silently.
func TestConnectionEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		baseURL  string
		wantHost string
		wantBase string
	}{
		{"gitlab saas", "gitlab", "", "gitlab.com", ""},
		{"bitbucket saas", "bitbucket", "", "bitbucket.org", ""},

		{"self-managed with scheme", "gitlab", "https://gitlab.internal", "gitlab.internal", "https://gitlab.internal"},
		{"self-managed without scheme", "gitlab", "gitlab.internal", "gitlab.internal", "https://gitlab.internal"},
		{"trailing slash", "gitlab", "https://gitlab.internal/", "gitlab.internal", "https://gitlab.internal"},
		{"port survives", "gitlab", "https://gitlab.internal:8443", "gitlab.internal:8443", "https://gitlab.internal:8443"},
		{"case folded host", "gitlab", "https://GitLab.Internal", "gitlab.internal", "https://GitLab.Internal"},
		{"http is preserved", "bitbucket", "http://bitbucket.internal", "bitbucket.internal", "http://bitbucket.internal"},
		{"whitespace", "gitlab", "  https://gitlab.internal  ", "gitlab.internal", "https://gitlab.internal"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, base, err := connectionEndpoint(tc.provider, tc.baseURL)
			if err != nil {
				t.Fatalf("connectionEndpoint(%q, %q): %v", tc.provider, tc.baseURL, err)
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if base != tc.wantBase {
				t.Errorf("base = %q, want %q", base, tc.wantBase)
			}
		})
	}
}

// An empty base URL must stay empty rather than becoming a literal. The providers read an
// absent base as "use the SaaS API", and Bitbucket additionally reads a present one as
// "this is Data Center" -- a different REST API, different payloads, different everything.
func TestSaaSConnectionHasNoBaseURL(t *testing.T) {
	for _, provider := range []string{"gitlab", "bitbucket"} {
		_, base, err := connectionEndpoint(provider, "")
		if err != nil {
			t.Fatalf("connectionEndpoint(%q, \"\"): %v", provider, err)
		}
		if base != "" {
			t.Errorf("%s SaaS connection got base URL %q, want empty", provider, base)
		}
	}
}

func TestConnectionEndpointRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"https://", "://nope", "http://"} {
		if host, base, err := connectionEndpoint("gitlab", bad); err == nil {
			t.Errorf("connectionEndpoint(gitlab, %q) = (%q, %q), want an error", bad, host, base)
		}
	}
}
