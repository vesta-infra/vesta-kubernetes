package middleware

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/rbac"
	"kubernetes.getvesta.sh/api/internal/scope"
)

// Access control at project and environment scope.
//
// What existed before: three global roles, a deny-list that let a viewer through whenever
// the role claim was missing, and a project role hard-coded to "owner" and checked in
// exactly three handlers. A developer could therefore change any app in any project.

// ContextEffectiveRole is where the resolved role is stashed, so a handler can refine a
// decision the gate made coarsely.
const (
	ContextEffectiveRole = "effectiveRole"
	ContextProjectID     = "resolvedProjectId"
	ContextEnvironment   = "resolvedEnvironment"
)

// RequireAccess gates a route on an action at the scope the request addresses.
//
// The scope is worked out from whatever the route carries: an explicit :projectId, or an
// :appId resolved to its project. The environment narrows it when the route names one.
func RequireAccess(database *db.DB, resolver *scope.Resolver, action rbac.Action) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userId")
		globalRole := c.GetString("role")

		// A global admin short-circuits before any lookup. Losing this would let an admin
		// lock themselves out of a project they are not a member of, with no way back.
		if globalRole == rbac.RoleAdmin {
			c.Set(ContextEffectiveRole, rbac.RoleAdmin)
			c.Next()
			return
		}

		// Fail closed. An absent identity is not an anonymous user with default rights; it
		// means the auth middleware did not run or did not finish, and the old deny-list
		// treated exactly that case as permission to proceed.
		if userID == "" {
			forbid(c, "not authenticated")
			return
		}

		projectID, environment, err := resolveScope(c, resolver)
		if err != nil {
			if errors.Is(err, errAppNotFound) {
				c.AbortWithStatusJSON(http.StatusNotFound,
					models.ErrorResponse{Code: 404, Message: "app not found"})
				return
			}
			// A scope that cannot be determined is a scope that cannot be checked.
			log.Printf("[rbac] could not resolve scope for %s: %v", c.FullPath(), err)
			forbid(c, "could not determine which project this request affects")
			return
		}

		role := effectiveRole(c, database, userID, globalRole, projectID, environment)

		c.Set(ContextEffectiveRole, role)
		c.Set(ContextProjectID, projectID)
		c.Set(ContextEnvironment, environment)

		if !rbac.Can(role, action) {
			forbid(c, "insufficient permissions: this needs "+rbac.RequiredRole(action)+
				" on "+describeScope(projectID, environment))
			return
		}
		c.Next()
	}
}

// effectiveRole combines the user's memberships with the instance's enforcement setting.
func effectiveRole(c *gin.Context, database *db.DB, userID, globalRole, projectID, environment string) string {
	// Enforcement off is the upgrade path. Existing installs have no project memberships at
	// all -- the only way to get one was a hard-coded "owner" -- so switching this on
	// without warning would lock every non-admin out of every project at once. While off,
	// the global role maps onto the new ladder exactly as it behaved before.
	if !database.GetBoolSetting(c.Request.Context(), db.SettingRBACEnforcement, false) {
		return rbac.LegacyFallback(globalRole)
	}

	projectRole, err := database.GetProjectMemberRole(c.Request.Context(), projectID, userID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		log.Printf("[rbac] project role lookup failed for %s/%s: %v", projectID, userID, err)
		return rbac.RoleNone
	}

	envRole := ""
	if environment != "" {
		envRole, err = database.GetProjectEnvRole(c.Request.Context(), projectID, environment, userID)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			log.Printf("[rbac] env role lookup failed for %s/%s/%s: %v", projectID, environment, userID, err)
			return rbac.RoleNone
		}
	}

	return rbac.EffectiveRole(globalRole, projectRole, envRole)
}

var errAppNotFound = errors.New("app not found")

// resolveScope works out which project and environment a request addresses.
func resolveScope(c *gin.Context, resolver *scope.Resolver) (projectID, environment string, err error) {
	environment = c.Param("env")
	if environment == "" {
		// Deploy-shaped routes carry the environment in the body, which middleware must
		// not consume -- reading it here would leave the handler with an empty body. Those
		// routes are gated at project level and check the environment in the handler.
		environment = c.Query("environment")
	}

	if p := c.Param("projectId"); p != "" {
		return p, environment, nil
	}

	appID := c.Param("appId")
	if appID == "" {
		return "", environment, errors.New("route carries neither a project nor an app")
	}
	if resolver == nil {
		return "", environment, errors.New("no scope resolver configured")
	}

	s, err := resolver.Resolve(c.Request.Context(), appID)
	if err != nil {
		return "", environment, errAppNotFound
	}
	if s.Project == "" {
		return "", environment, errors.New("app declares no project")
	}
	return s.Project, environment, nil
}

func describeScope(projectID, environment string) string {
	if environment != "" {
		return projectID + "/" + environment
	}
	return projectID
}

func forbid(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: message})
}

// EffectiveRoleFor computes a role outside a gate, for handlers that need to refine a
// decision -- the deploy routes, which carry their environment in the body.
func EffectiveRoleFor(c *gin.Context, database *db.DB, projectID, environment string) string {
	if c.GetString("role") == rbac.RoleAdmin {
		return rbac.RoleAdmin
	}
	userID := c.GetString("userId")
	if userID == "" {
		return rbac.RoleNone
	}
	return effectiveRole(c, database, userID, c.GetString("role"), projectID, environment)
}
