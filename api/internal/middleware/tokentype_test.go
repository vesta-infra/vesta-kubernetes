package middleware

import (
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// realRoutePatterns are gin route patterns lifted from cmd/main.go. Every one of them
// must be closed to a partially-authenticated token. If someone adds a route to the
// authenticated group and it somehow becomes reachable mid-authentication, this is what
// catches it.
var realRoutePatterns = []string{
	"/api/v1/users",
	"/api/v1/auth/register",
	"/api/v1/auth/tokens",
	"/api/v1/auth/tokens/:id",
	"/api/v1/teams",
	"/api/v1/teams/:teamId",
	"/api/v1/projects",
	"/api/v1/projects/:projectId",
	"/api/v1/projects/:projectId/apps",
	"/api/v1/apps",
	"/api/v1/apps/:appId",
	"/api/v1/apps/:appId/deploy",
	"/api/v1/apps/:appId/rollback",
	"/api/v1/apps/:appId/restart",
	"/api/v1/apps/:appId/scale",
	"/api/v1/apps/:appId/exec",
	"/api/v1/apps/:appId/logs/ws",
	"/api/v1/apps/:appId/files",
	"/api/v1/apps/:appId/files/write",
	"/api/v1/apps/:appId/builds",
	"/api/v1/secrets",
	"/api/v1/audit-logs",
	"/api/v1/activity",
	"/api/v1/webhook-deliveries",
	"/api/v1/settings/github-app",
}

// The single most important test in this change: the allowlist defaults to closed.
func TestAllowedForChallengeIsFailClosed(t *testing.T) {
	for _, pattern := range realRoutePatterns {
		t.Run(pattern, func(t *testing.T) {
			for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
				if AllowedForChallenge(method, pattern, TokenTypeMFAChallenge) {
					t.Errorf("challenge token may reach %s %s", method, pattern)
				}
				if AllowedForChallenge(method, pattern, TokenTypeMFAEnroll) {
					t.Errorf("enroll token may reach %s %s", method, pattern)
				}
			}
		})
	}
}

// A challenge token exists only to be exchanged for a session.
func TestAllowedForChallengeAllowsOnlyVerificationRoutes(t *testing.T) {
	allowed := []string{
		"/api/v1/auth/mfa/verify",
		"/api/v1/auth/mfa/webauthn/authenticate/begin",
		"/api/v1/auth/mfa/webauthn/authenticate/finish",
	}
	for _, p := range allowed {
		if !AllowedForChallenge("POST", p, TokenTypeMFAChallenge) {
			t.Errorf("challenge token cannot reach %s, which it needs", p)
		}
	}

	// Enrollment routes are a different token type: a challenge token must not be able to
	// enrol a new factor, or an attacker with only a password could add their own.
	for _, p := range []string{
		"/api/v1/auth/mfa/totp/enroll",
		"/api/v1/auth/mfa/totp/confirm",
		"/api/v1/auth/mfa/webauthn/register/begin",
	} {
		if AllowedForChallenge("POST", p, TokenTypeMFAChallenge) {
			t.Errorf("challenge token may reach enrollment route %s", p)
		}
	}
}

func TestAllowedForChallengeEnrollScope(t *testing.T) {
	allowed := []struct{ method, pattern string }{
		{"GET", "/api/v1/auth/mfa/status"},
		{"POST", "/api/v1/auth/mfa/totp/enroll"},
		{"POST", "/api/v1/auth/mfa/totp/confirm"},
		{"POST", "/api/v1/auth/mfa/webauthn/register/begin"},
		{"POST", "/api/v1/auth/mfa/webauthn/register/finish"},
		{"GET", "/api/v1/users/me"}, // so the enrollment screen can greet the user
	}
	for _, a := range allowed {
		if !AllowedForChallenge(a.method, a.pattern, TokenTypeMFAEnroll) {
			t.Errorf("enroll token cannot reach %s %s, which it needs", a.method, a.pattern)
		}
	}

	// An enroll token must not be able to complete a challenge it never started.
	if AllowedForChallenge("POST", "/api/v1/auth/mfa/verify", TokenTypeMFAEnroll) {
		t.Error("enroll token may reach the verification endpoint")
	}
	// Nor change the password, which would let a half-authenticated caller take over.
	if AllowedForChallenge("PUT", "/api/v1/users/me/password", TokenTypeMFAEnroll) {
		t.Error("enroll token may change the password")
	}
}

func TestAllowedForChallengeSessionReachesEverything(t *testing.T) {
	for _, p := range realRoutePatterns {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			if !AllowedForChallenge(method, p, TokenTypeSession) {
				t.Errorf("a full session was denied %s %s", method, p)
			}
		}
	}
}

// Matching happens on gin's resolved route pattern, so these variants never reach the
// comparison. Asserting it anyway pins the behaviour if someone swaps FullPath() for the
// raw URL path.
func TestAllowedForChallengeRejectsPathVariants(t *testing.T) {
	variants := []string{
		"/api/v1/auth/mfa/verify/",
		"//api/v1/auth/mfa/verify",
		"/api/v1/auth/mfa/../mfa/verify",
		"/api/v1/auth/mfa/%2e%2e/verify",
		"/API/V1/AUTH/MFA/VERIFY",
		"/api/v1/auth/mfa/verify?x=1",
		" /api/v1/auth/mfa/verify",
		"",
	}
	for _, v := range variants {
		t.Run(v, func(t *testing.T) {
			if AllowedForChallenge("POST", v, TokenTypeMFAChallenge) {
				t.Errorf("variant %q was accepted", v)
			}
		})
	}
}

func TestAllowedForChallengeRejectsUnknownType(t *testing.T) {
	if AllowedForChallenge("POST", "/api/v1/auth/mfa/verify", TokenType("something-else")) {
		t.Error("an unrecognised token type was allowed through")
	}
	if AllowedForChallenge("GET", "/api/v1/apps", TokenType("")) {
		t.Error("an empty token type was allowed through")
	}
}

func TestClassifyToken(t *testing.T) {
	cases := []struct {
		name    string
		claims  jwt.MapClaims
		want    TokenType
		wantErr bool
	}{
		{"session", jwt.MapClaims{"typ": "session"}, TokenTypeSession, false},
		{"challenge", jwt.MapClaims{"typ": "mfa_challenge"}, TokenTypeMFAChallenge, false},
		{"enroll", jwt.MapClaims{"typ": "mfa_enroll"}, TokenTypeMFAEnroll, false},
		{"missing typ", jwt.MapClaims{"sub": "u1"}, "", true},
		{"empty typ", jwt.MapClaims{"typ": ""}, "", true},
		{"non-string typ", jwt.MapClaims{"typ": 42}, "", true},
		{"nil typ", jwt.MapClaims{"typ": nil}, "", true},
		{"unknown typ", jwt.MapClaims{"typ": "root"}, "", true},
		{"case variant is not a match", jwt.MapClaims{"typ": "SESSION"}, "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ClassifyToken(c.claims)
			if (err != nil) != c.wantErr {
				t.Fatalf("ClassifyToken(%v) error = %v, wantErr %v", c.claims, err, c.wantErr)
			}
			if !c.wantErr && got != c.want {
				t.Errorf("ClassifyToken = %q, want %q", got, c.want)
			}
		})
	}
}

// A token with no typ predates the claim. Treating it as a session would reopen the hole
// for the lifetime of every already-issued token.
func TestClassifyTokenRejectsMissingTypClaim(t *testing.T) {
	_, err := ClassifyToken(jwt.MapClaims{"sub": "u1", "role": "admin"})
	if !errors.Is(err, ErrMissingTokenType) {
		t.Errorf("error = %v, want ErrMissingTokenType", err)
	}
}

// /api/v1/users/me is registered for GET (read profile) and PUT (update it). An enroll
// token needs the read so the enrollment screen can address the user by name, but must
// never be able to change the email address on an account it has not finished
// authenticating into.
func TestEnrollTokenMayReadButNotWriteTheProfile(t *testing.T) {
	if !AllowedForChallenge("GET", "/api/v1/users/me", TokenTypeMFAEnroll) {
		t.Error("enroll token cannot read /users/me, which the enrollment screen needs")
	}
	for _, method := range []string{"PUT", "POST", "PATCH", "DELETE"} {
		if AllowedForChallenge(method, "/api/v1/users/me", TokenTypeMFAEnroll) {
			t.Errorf("enroll token may %s /users/me", method)
		}
	}
}

// Method matching must not be defeated by casing, since it is compared as a string.
func TestAllowedForChallengeMethodIsCaseInsensitive(t *testing.T) {
	if !AllowedForChallenge("post", "/api/v1/auth/mfa/verify", TokenTypeMFAChallenge) {
		t.Error("lowercase method was rejected")
	}
	if !AllowedForChallenge("get", "/api/v1/users/me", TokenTypeMFAEnroll) {
		t.Error("lowercase method was rejected")
	}
}
