package handlers

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

// checkWebSocketOrigin guards the log-streaming and pod-exec upgrades against cross-site
// connections.
//
// This used to return true unconditionally. That is not directly exploitable while the
// session token travels in a query parameter rather than a cookie, but the same upgrader
// backs an interactive shell into production pods, so it becomes a remote-shell CSRF the
// moment anything attaches ambient credentials.
func checkWebSocketOrigin(r *http.Request) bool {
	return allowedWebSocketOrigin(
		r.Header.Get("Origin"),
		r.Host,
		splitAllowedOrigins(os.Getenv("VESTA_ALLOWED_ORIGINS")),
	)
}

// allowedWebSocketOrigin reports whether an Origin header value may open a socket to a
// request that arrived at requestHost.
//
// An absent Origin is allowed: non-browser clients (the CLI, websocat, kubectl port
// forwards) do not send one, and only browsers attach credentials automatically. When
// present, the origin's host must either match the host the request arrived at - the
// same-origin case, which covers the normal deployment where the UI proxies /api - or
// appear in the configured allowlist.
func allowedWebSocketOrigin(origin, requestHost string, allowlist []string) bool {
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	if requestHost != "" && strings.EqualFold(u.Host, requestHost) {
		return true
	}

	for _, allowed := range allowlist {
		// Accept either a full origin ("https://ui.example.com") or a bare host
		// ("ui.example.com"), since operators reasonably write both.
		if strings.EqualFold(allowed, origin) {
			return true
		}
		if strings.EqualFold(allowed, u.Host) {
			return true
		}
	}
	return false
}

// splitAllowedOrigins parses the comma-separated VESTA_ALLOWED_ORIGINS value, dropping
// blank entries and surrounding whitespace.
func splitAllowedOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(strings.TrimRight(p, "/")); p != "" {
			out = append(out, p)
		}
	}
	return out
}
