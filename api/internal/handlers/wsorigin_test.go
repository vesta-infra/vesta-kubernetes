package handlers

import "testing"

func TestAllowedWebSocketOrigin(t *testing.T) {
	cases := []struct {
		name      string
		origin    string
		host      string
		allowlist []string
		want      bool
	}{
		{"no origin is a non-browser client", "", "vesta.example.com", nil, true},
		{"same origin", "https://vesta.example.com", "vesta.example.com", nil, true},
		{"same origin with port", "http://localhost:3000", "localhost:3000", nil, true},
		{"host comparison is case insensitive", "https://Vesta.Example.com", "vesta.example.com", nil, true},
		{"foreign origin rejected", "https://evil.tld", "vesta.example.com", nil, false},
		{"port mismatch is a different origin", "http://localhost:3001", "localhost:3000", nil, false},
		{"allowlisted full origin", "https://ui.example.com", "api.example.com", []string{"https://ui.example.com"}, true},
		{"allowlisted bare host", "https://ui.example.com", "api.example.com", []string{"ui.example.com"}, true},
		{"allowlist miss", "https://evil.tld", "api.example.com", []string{"ui.example.com"}, false},
		{"malformed origin rejected", "://not a url", "vesta.example.com", nil, false},
		{"origin without host rejected", "https://", "vesta.example.com", nil, false},
		{"null origin rejected", "null", "vesta.example.com", nil, false},
		{"empty host does not match empty origin host", "https://", "", nil, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allowedWebSocketOrigin(c.origin, c.host, c.allowlist); got != c.want {
				t.Errorf("allowedWebSocketOrigin(%q, %q, %v) = %v, want %v",
					c.origin, c.host, c.allowlist, got, c.want)
			}
		})
	}
}

// The same upgrader serves interactive pod exec, so a foreign origin must never be
// accepted just because no allowlist happens to be configured.
func TestAllowedWebSocketOriginFailsClosedWithoutAllowlist(t *testing.T) {
	if allowedWebSocketOrigin("https://attacker.example", "vesta.example.com", nil) {
		t.Error("a foreign origin was accepted with no allowlist configured")
	}
}

func TestSplitAllowedOrigins(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"empty", "", 0},
		{"whitespace only", "   ", 0},
		{"single", "https://ui.example.com", 1},
		{"multiple with spaces", "https://a.example.com, https://b.example.com", 2},
		{"blank entries dropped", "https://a.example.com,,  ,https://b.example.com", 2},
		{"trailing slash trimmed", "https://a.example.com/", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := splitAllowedOrigins(c.raw); len(got) != c.want {
				t.Errorf("splitAllowedOrigins(%q) = %v (len %d), want len %d", c.raw, got, len(got), c.want)
			}
		})
	}
}

func TestSplitAllowedOriginsTrimsTrailingSlash(t *testing.T) {
	got := splitAllowedOrigins("https://ui.example.com/")
	if len(got) != 1 || got[0] != "https://ui.example.com" {
		t.Errorf("expected the trailing slash trimmed so it matches an Origin header, got %v", got)
	}
}
