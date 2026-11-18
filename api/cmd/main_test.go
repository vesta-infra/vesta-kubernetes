package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/handlers"
	"kubernetes.getvesta.sh/api/internal/middleware"
)

// buildTestRouter registers what production registers, against a nil handler. Method
// values on a nil receiver are fine here because the routes are inspected, never served.
func buildTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	auth := r.Group("/api/v1")

	// Registered by main before the MFA block. Order and coexistence both matter:
	// /users/me is a static segment while the admin reset is /users/:userId/mfa, and gin
	// panics on some static-versus-wildcard combinations at the same position.
	auth.GET("/users/me", func(*gin.Context) {})
	auth.PUT("/users/me", func(*gin.Context) {})
	auth.PUT("/users/me/password", func(*gin.Context) {})
	auth.GET("/users", func(*gin.Context) {})

	registerMFARoutes(auth, (*handlers.Handler)(nil))
	return r
}

// The failure this guards against is not a compile error: it is a login that reaches
// "enter your code" and then 404s, because a path in the allowlist was never registered.
func TestEveryPartialAuthRouteIsRegistered(t *testing.T) {
	if err := verifyPartialAuthRoutes(buildTestRouter(t)); err != nil {
		t.Fatalf("allowlist and router disagree: %v", err)
	}
}

func TestVerifyPartialAuthRoutesCatchesAMissingRoute(t *testing.T) {
	// A router with none of the MFA routes must be rejected, or the check above proves
	// nothing.
	gin.SetMode(gin.TestMode)
	bare := gin.New()
	bare.Group("/api/v1")

	err := verifyPartialAuthRoutes(bare)
	if err == nil {
		t.Fatal("expected an empty router to be rejected")
	}
	if !strings.Contains(err.Error(), "/api/v1/auth/mfa/verify") {
		t.Errorf("error should name the missing route, got: %v", err)
	}
}

// The allowlist distinguishes methods, so the router must too: /users/me is registered
// for both GET and PUT, and only GET may be reached mid-enrollment.
func TestPartialAuthRoutesCarryMethods(t *testing.T) {
	for _, spec := range middleware.PartialAuthRoutes() {
		if spec.Method == "" {
			t.Errorf("route %q has no method", spec.Pattern)
		}
		if !strings.HasPrefix(spec.Pattern, "/api/v1/") {
			t.Errorf("route %q is not a full gin pattern; AllowedForChallenge compares against c.FullPath()", spec.Pattern)
		}
	}
}

// Gin panics on conflicting static and wildcard segments at the same position, and the
// admin reset puts /users/:userId/mfa alongside the existing /users/me. A panic here
// would take down the whole API at startup, not just this endpoint.
func TestUserRoutesCoexistWithAdminReset(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("route registration panicked: %v", r)
		}
	}()

	r := buildTestRouter(t)

	var found bool
	for _, route := range r.Routes() {
		if route.Method == "DELETE" && route.Path == "/api/v1/users/:userId/mfa" {
			found = true
		}
	}
	if !found {
		t.Fatal("admin MFA reset route was not registered")
	}
}

// Both webhook paths must coexist.
//
// /webhooks/:provider is what hooks created before connections existed still POST to;
// /webhooks/:provider/:connectionId is what new connections register. gin panics at
// registration on some wildcard combinations, and a panic here takes the API down at
// startup rather than failing a request -- so it has to be caught at build time, not in a
// cluster.
func TestBothWebhookRoutesCoexist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registering both webhook routes panicked: %v", r)
		}
	}()

	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.POST("/webhooks/:provider", func(*gin.Context) {})
	v1.POST("/webhooks/:provider/:connectionId", func(*gin.Context) {})

	var legacy, scoped bool
	for _, route := range r.Routes() {
		switch route.Path {
		case "/api/v1/webhooks/:provider":
			legacy = route.Method == "POST"
		case "/api/v1/webhooks/:provider/:connectionId":
			scoped = route.Method == "POST"
		}
	}
	if !legacy {
		t.Error("the connection-less webhook route is missing; every webhook created " +
			"before this release points at it")
	}
	if !scoped {
		t.Error("the per-connection webhook route is missing")
	}
}

// Every project-scoped and app-scoped route must name an access gate.
//
// This reads main.go as source text because gin cannot answer it at runtime: RouteInfo
// exposes only the final handler's name, not the middleware chain, so an ungated route is
// indistinguishable from a gated one once registered. The same technique guards the reauth
// calls in the MFA handlers.
//
// The failure it prevents is quiet. A new /apps/:appId route added without a gate is not a
// broken build or a failing request — it is an endpoint anyone authenticated can call,
// discovered whenever someone thinks to look.
func TestEveryScopedRouteCarriesAGate(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Skipf("cannot read main.go: %v", err)
	}

	gates := []string{"canRead", "canDeploy", "canWrite", "canSecrets", "canAdmin",
		// Routes gated to global admins need no project scope: they are not about one
		// project.
		`RequireRole("admin")`}

	route := regexp.MustCompile(`auth\.(GET|POST|PUT|DELETE)\("(/(?:apps/:appId|projects/:projectId)[^"]*)"`)

	var ungated []string
	for _, line := range strings.Split(string(src), "\n") {
		m := route.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		gated := false
		for _, g := range gates {
			if strings.Contains(line, g) {
				gated = true
				break
			}
		}
		if !gated {
			ungated = append(ungated, m[1]+" "+m[2])
		}
	}

	for _, r := range ungated {
		t.Errorf("route %s carries no access gate; add one of canRead/canDeploy/canWrite/"+
			"canSecrets/canAdmin, or RequireRole(\"admin\") if it is not project-scoped", r)
	}
}
