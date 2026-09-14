package handlers

import (
	"github.com/gin-gonic/gin"

	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/middleware"
	"kubernetes.getvesta.sh/api/internal/rbac"
)

// Who may see and use a stored credential.
//
// Registry credentials were instance-wide with no scoping at all: every developer could
// list, use and delete every credential on the instance. Making them project-scoped outright
// would have been a breaking change -- every credential that exists today is unscoped, and
// reclassifying them would cut apps off from the credentials they already pull with.
//
// So scope is a field with a backwards-compatible default. An existing credential has no
// scope and stays global; an instance that wants isolation sets a platform default, which
// applies to NEW secrets only and never reclassifies what is already there.

const (
	SecretScopeGlobal  = "global"
	SecretScopeProject = "project"
)

// ResolveSecretScope decides a secret's effective scope.
//
// The secret's own value wins, then the platform default, then global. Anything
// unrecognised resolves to global rather than being guessed at: a typo in platform config
// must not silently hide credentials that apps depend on.
func ResolveSecretScope(specScope, platformDefault string) string {
	for _, candidate := range []string{specScope, platformDefault} {
		if candidate == SecretScopeProject {
			return SecretScopeProject
		}
		if candidate == SecretScopeGlobal {
			return SecretScopeGlobal
		}
	}
	return SecretScopeGlobal
}

// CanAccessSecret reports whether a caller may act on a credential.
//
// role is the caller's effective role in the secret's project, already resolved. Global
// secrets are allowed through on the strength of the route's own gate, which is what every
// caller got before scoping existed.
func CanAccessSecret(scope, secretProject, role string, action rbac.Action) bool {
	if scope != SecretScopeProject {
		return true
	}
	if secretProject == "" {
		// Project-scoped and naming no project: there is no membership to check, so there
		// is no way to establish access. Fails closed. The API refuses to create this
		// shape, so reaching it means the object was written directly.
		return false
	}
	return rbac.Can(role, action)
}

// secretScopeOf reads the scope and project off a loaded VestaSecret, resolving the platform
// default. Returns the effective scope and the project it belongs to.
func (h *Handler) secretScopeOf(c *gin.Context, spec map[string]interface{}) (scope, project string) {
	return ResolveSecretScope(
		getNestedString(spec, "scope"),
		h.defaultSecretScope(c),
	), getNestedString(spec, "project")
}

// mayAccessSecret is the check the handlers call: resolve the caller's role in the secret's
// project and decide.
func (h *Handler) mayAccessSecret(c *gin.Context, spec map[string]interface{}, action rbac.Action) bool {
	scope, project := h.secretScopeOf(c, spec)
	if scope != SecretScopeProject {
		return true
	}
	role := middleware.EffectiveRoleFor(c, h.DB, project, "")
	return CanAccessSecret(scope, project, role, action)
}

// defaultSecretScope reads the platform default. A missing or unreadable config means
// global, which is the behaviour every install had before this existed.
func (h *Handler) defaultSecretScope(c *gin.Context) string {
	cfg, err := h.K8s.GetClusterResource(c.Request.Context(), k8s.VestaConfigGVR, vestaConfigName)
	if err != nil {
		return SecretScopeGlobal
	}
	spec, _, _ := unstructuredNestedMap(cfg.Object, "spec")
	security, _, _ := unstructuredNestedMap(spec, "security")
	return ResolveSecretScope("", getNestedString(security, "defaultSecretScope"))
}
