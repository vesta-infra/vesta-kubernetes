package main

import (
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
