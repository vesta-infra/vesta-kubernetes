package mfa

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// RelyingParty is the identity WebAuthn binds a credential to.
type RelyingParty struct {
	// ID is the bare registrable domain, no scheme and no port.
	ID string
	// Origin is the full scheme://host[:port] the browser reports.
	Origin string
}

// ResolveRelyingParty works out the RP ID and expected origin for a WebAuthn ceremony.
//
// Two things make this harder than reading a header:
//
// WebAuthn validates against the *browser's* origin, not the API's. In the default chart
// the UI and API sit on different hostnames, and in local development the Vite proxy sets
// changeOrigin, rewriting Host to localhost:8090 while the browser is on localhost:3000.
// Deriving the origin from the API's own Host therefore fails in both cases. The Origin
// header is what the browser actually sends and both proxies forward it untouched, so it
// is preferred and Host is only a fallback.
//
// X-Forwarded-Host is deliberately never consulted: nginx does not set it, so it arrives
// exactly as a client typed it.
//
// When allowlist is non-empty the resolved origin must appear in it. When it is empty any
// syntactically valid registrable domain is accepted, which is the zero-configuration
// path - a forged Host then lets someone register a credential under an RP ID of their
// choosing, but it does not let them authenticate as anyone, because the browser enforces
// the real origin and the ceremony simply fails.
func ResolveRelyingParty(originHeader, requestHost string, tls bool, allowlist []string) (RelyingParty, error) {
	origin := strings.TrimSpace(originHeader)
	if origin == "" {
		if requestHost == "" {
			return RelyingParty{}, fmt.Errorf("mfa: cannot determine relying party: no Origin header and no Host")
		}
		scheme := "http"
		if tls {
			scheme = "https"
		}
		origin = scheme + "://" + requestHost
	}

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return RelyingParty{}, fmt.Errorf("mfa: unusable origin %q", originHeader)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return RelyingParty{}, fmt.Errorf("mfa: origin %q must be http or https", origin)
	}

	host := strings.ToLower(u.Hostname())
	if err := validateRPID(host); err != nil {
		return RelyingParty{}, err
	}

	// A secure context is required by the browser anyway; localhost is the exception it
	// carves out. Failing here produces a clear message instead of an opaque browser
	// rejection later.
	if u.Scheme != "https" && !isLoopbackHost(host) {
		return RelyingParty{}, fmt.Errorf("mfa: passkeys require HTTPS (or localhost); %q is not a secure context", origin)
	}

	resolved := RelyingParty{
		ID: host,
		// Lowercase the whole origin: DNS is case-insensitive and browsers send lowercase,
		// so normalising here keeps the stored value comparable to what arrives later.
		Origin: strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host),
	}

	if len(allowlist) > 0 && !originInAllowlist(resolved, allowlist) {
		return RelyingParty{}, fmt.Errorf("mfa: origin %q is not in the configured allowlist", resolved.Origin)
	}
	return resolved, nil
}

func originInAllowlist(rp RelyingParty, allowlist []string) bool {
	for _, entry := range allowlist {
		entry = strings.ToLower(strings.TrimRight(strings.TrimSpace(entry), "/"))
		if entry == "" {
			continue
		}
		if entry == rp.Origin || entry == rp.ID {
			return true
		}
		// Tolerate an allowlist entry written with a scheme but a different one.
		if u, err := url.Parse(entry); err == nil && u.Host != "" &&
			strings.ToLower(u.Hostname()) == rp.ID {
			return true
		}
	}
	return false
}

// validateRPID rejects hosts that cannot serve as a WebAuthn relying party.
func validateRPID(host string) error {
	switch {
	case host == "":
		return fmt.Errorf("mfa: empty relying party host")
	case len(host) > 253:
		return fmt.Errorf("mfa: relying party host is too long")
	case strings.Contains(host, ".."):
		return fmt.Errorf("mfa: relying party host %q contains an empty label", host)
	case strings.HasPrefix(host, ".") || strings.HasSuffix(host, "."):
		return fmt.Errorf("mfa: relying party host %q has a leading or trailing dot", host)
	}

	// Browsers reject IP literals as RP IDs outright, so catch it here with a message
	// that explains why rather than letting the ceremony fail opaquely.
	if net.ParseIP(host) != nil && !isLoopbackHost(host) {
		return fmt.Errorf("mfa: %q is an IP address; passkeys need a domain name", host)
	}

	for _, r := range host {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.'
		if !isAllowed {
			return fmt.Errorf("mfa: relying party host %q contains an invalid character", host)
		}
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
