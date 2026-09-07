package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// TokenType distinguishes a full session from the short-lived tokens issued part-way
// through authentication.
//
// Without this every validly-signed JWT was a full session, so a token handed out after
// the password check but before the second factor would have been honoured by every
// authenticated endpoint - including the one that mints long-lived API keys. The type is
// the whole reason a second factor can be enforced at all.
type TokenType string

const (
	// TokenTypeSession is a fully authenticated session.
	TokenTypeSession TokenType = "session"

	// TokenTypeMFAChallenge is issued after a correct password when the user holds a
	// second factor. It may only reach the verification endpoints.
	TokenTypeMFAChallenge TokenType = "mfa_challenge"

	// TokenTypeMFAEnroll is issued to a user who must enrol before continuing. It may
	// only reach the enrollment endpoints and the current-user lookup.
	TokenTypeMFAEnroll TokenType = "mfa_enroll"
)

// ContextTokenType is the gin context key holding the caller's token type.
const ContextTokenType = "tokenType"

// ErrMissingTokenType means the token carries no typ claim.
//
// Treated as invalid rather than assumed to be a session: fail-closed is the only safe
// default, and rotating JWT_SECRET already invalidated every token minted before the
// claim existed, so nothing legitimate is affected.
var ErrMissingTokenType = fmt.Errorf("token has no typ claim")

// route is an HTTP method paired with a gin route pattern.
//
// The method matters: /api/v1/users/me is registered for both GET and PUT, so a
// path-only allowlist would let a token that has not finished authenticating change the
// account's email address.
type route struct {
	method  string
	pattern string
}

// challengeRoutes are what an mfa_challenge token may reach: the endpoints that exchange
// a second factor for a real session, and nothing else.
var challengeRoutes = map[route]bool{
	{"POST", "/api/v1/auth/mfa/verify"}:                       true,
	{"POST", "/api/v1/auth/mfa/webauthn/authenticate/begin"}:  true,
	{"POST", "/api/v1/auth/mfa/webauthn/authenticate/finish"}: true,
}

// enrollRoutes are what an mfa_enroll token may reach. GET /users/me is included so the
// enrollment screen can greet the user by name - and only GET, so the same token cannot
// edit the profile it is displaying.
var enrollRoutes = map[route]bool{
	{"GET", "/api/v1/auth/mfa/status"}:                    true,
	{"POST", "/api/v1/auth/mfa/totp/enroll"}:              true,
	{"POST", "/api/v1/auth/mfa/totp/confirm"}:             true,
	{"POST", "/api/v1/auth/mfa/webauthn/register/begin"}:  true,
	{"POST", "/api/v1/auth/mfa/webauthn/register/finish"}: true,
	{"GET", "/api/v1/users/me"}:                           true,
}

// RouteSpec names one method/pattern pair a partial token may reach.
type RouteSpec struct {
	Method  string
	Pattern string
}

// PartialAuthRoutes returns every route reachable with a token that has not finished
// authenticating.
//
// Exported so startup can assert these routes actually exist. The allowlist and the
// router are edited in different files, and either drifting from the other fails
// silently: an allowlisted path that was never registered is a dead end mid-login, and a
// registered path missing from the allowlist is unreachable exactly when it is needed.
func PartialAuthRoutes() []RouteSpec {
	out := make([]RouteSpec, 0, len(challengeRoutes)+len(enrollRoutes))
	for _, set := range []map[route]bool{challengeRoutes, enrollRoutes} {
		for r := range set {
			out = append(out, RouteSpec{Method: r.method, Pattern: r.pattern})
		}
	}
	return out
}

// ClassifyToken reads the token type from JWT claims, failing closed on anything it does
// not recognise.
func ClassifyToken(claims jwt.MapClaims) (TokenType, error) {
	raw, ok := claims["typ"]
	if !ok {
		return "", ErrMissingTokenType
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		return "", ErrMissingTokenType
	}

	switch t := TokenType(s); t {
	case TokenTypeSession, TokenTypeMFAChallenge, TokenTypeMFAEnroll:
		return t, nil
	default:
		return "", fmt.Errorf("unrecognised token type %q", s)
	}
}

// AllowedForChallenge reports whether a partially-authenticated token may reach a
// route, matched on both HTTP method and route pattern.
//
// routePattern must be gin's matched route pattern (c.FullPath()), never the raw request
// path. Gin has already resolved the route by that point, so the comparison cannot be
// fooled by a trailing slash, a doubled separator, percent-encoding or casing.
//
// The default is false. Any route not named here is closed to a partial token, which
// means an endpoint added later is protected without anyone remembering to think about
// it.
func AllowedForChallenge(method, routePattern string, typ TokenType) bool {
	r := route{method: strings.ToUpper(method), pattern: routePattern}
	switch typ {
	case TokenTypeSession:
		return true
	case TokenTypeMFAChallenge:
		return challengeRoutes[r]
	case TokenTypeMFAEnroll:
		return enrollRoutes[r]
	default:
		return false
	}
}

// RequireFullSession rejects anything that is not a fully authenticated session.
//
// Applied once to the authenticated route group, before any route is registered on it,
// so every existing and future endpoint in that group is covered - including the
// WebSocket routes that accept their token from a query parameter.
//
// It returns 403, never 401: the UI's shared fetch helper clears storage and hard-redirects
// to /login on any 401, which would destroy a challenge exchange mid-flow.
func RequireFullSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, exists := c.Get(ContextTokenType)
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "authentication is incomplete",
			})
			return
		}

		typ, _ := raw.(TokenType)
		// Defer to the same allowlist AuthRequired uses, so the two gates cannot disagree.
		// A few routes inside the authenticated group are deliberately reachable
		// mid-authentication - reading your own profile so the enrollment screen can
		// address you by name, for one - and hard-coding "session only" here would
		// contradict that.
		if typ != TokenTypeSession && !AllowedForChallenge(c.Request.Method, c.FullPath(), typ) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "two-factor authentication must be completed first",
				"reason":  string(typ),
			})
			return
		}
		c.Next()
	}
}
