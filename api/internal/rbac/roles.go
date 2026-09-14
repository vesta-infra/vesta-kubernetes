// Package rbac holds the role model: who may do what, at which scope.
//
// Vesta had three flat global roles -- admin, developer, viewer -- and one project role that
// was hard-coded to "owner". A developer could therefore touch every project on the
// instance, and there was no way to say "deploy to staging but not production", which is the
// distinction most teams actually want.
//
// Everything here is pure. The decision of whether a request is allowed is a function of
// three strings and an action, and keeping it that way is what makes the whole matrix
// testable without a database or a cluster.
package rbac

import "strings"

// Roles, weakest first. The order is the hierarchy: a role permits everything the roles
// below it permit.
const (
	RoleNone       = ""
	RoleViewer     = "viewer"
	RoleDeployer   = "deployer"
	RoleMaintainer = "maintainer"
	RoleOwner      = "owner"

	// RoleAdmin is global only. It is not a project or environment role, and appears here
	// because the global role is compared against the same ladder.
	RoleAdmin = "admin"

	// RoleDeveloper is the legacy global role. It grants project-level deploy rights
	// everywhere while enforcement is off, and nothing by itself once it is on.
	RoleDeveloper = "developer"
)

// rank orders roles. An unknown role ranks below viewer rather than above owner: a typo in a
// role column must reduce access, never grant it.
var rank = map[string]int{
	RoleNone:       0,
	RoleViewer:     1,
	RoleDeployer:   2,
	RoleMaintainer: 3,
	RoleOwner:      4,
	RoleAdmin:      5,
}

// Rank returns a role's position in the hierarchy, 0 for anything unrecognised.
func Rank(role string) int {
	return rank[strings.ToLower(strings.TrimSpace(role))]
}

// Valid reports whether a role may be stored against a project or environment.
//
// admin and developer are deliberately excluded: they are global roles, and accepting one
// here would create a project membership that reads as more powerful than owner.
func Valid(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleViewer, RoleDeployer, RoleMaintainer, RoleOwner:
		return true
	}
	return false
}

// AssignableRoles is what the UI offers, strongest first.
func AssignableRoles() []string {
	return []string{RoleOwner, RoleMaintainer, RoleDeployer, RoleViewer}
}

// EffectiveRole resolves the role a user holds for a particular environment.
//
// Precedence, and each part earns its place:
//
//   - A global admin always wins. Losing that would let an admin lock themselves out of a
//     project they are not a member of, with no way back.
//   - An environment role overrides the project role in BOTH directions. Raising it is the
//     obvious case; lowering it is the one that makes "maintainer on the project, viewer on
//     production" expressible, which is the whole point of environment scope.
//   - Otherwise the project role applies.
//
// An environment role is only consulted when one was explicitly recorded; absent means
// "inherit", not "none", or every environment would need a row before anyone could deploy.
func EffectiveRole(global, project, env string) string {
	if strings.EqualFold(strings.TrimSpace(global), RoleAdmin) {
		return RoleAdmin
	}
	if env != "" {
		return strings.ToLower(strings.TrimSpace(env))
	}
	return strings.ToLower(strings.TrimSpace(project))
}

// Allows reports whether a role is at least the role required.
func Allows(role, required string) bool {
	return Rank(role) >= Rank(required) && Rank(required) > 0
}

// Action names a thing a request wants to do. They are coarse on purpose: a matrix with one
// row per endpoint is a matrix nobody keeps correct.
type Action string

const (
	// ActionRead is anything that returns configuration or status.
	ActionRead Action = "read"
	// ActionDeploy is triggering a rollout, build, restart or scale. It changes what runs
	// without changing what is configured.
	ActionDeploy Action = "deploy"
	// ActionWrite is changing an app or project's configuration.
	ActionWrite Action = "write"
	// ActionSecrets is reading or writing secret values, and shell or file access to a
	// running pod -- which is the same thing by another route.
	ActionSecrets Action = "secrets"
	// ActionAdmin is changing who has access.
	ActionAdmin Action = "admin"
)

// required maps an action to the weakest role that may perform it.
//
// Secrets sit above deploy and write on purpose. Reading a secret, execing into a pod and
// reading a file from its filesystem are the same capability wearing three hats, so they
// share a level, and it is a higher one than deploying code.
var required = map[Action]string{
	ActionRead:    RoleViewer,
	ActionDeploy:  RoleDeployer,
	ActionWrite:   RoleMaintainer,
	ActionSecrets: RoleMaintainer,
	ActionAdmin:   RoleOwner,
}

// Can reports whether a role may perform an action.
func Can(role string, action Action) bool {
	need, known := required[action]
	if !known {
		// An action nobody has described is refused. Defaulting to allowed would mean a
		// new endpoint is ungated until somebody remembers to add it here.
		return false
	}
	return Allows(role, need)
}

// RequiredRole returns the weakest role that may perform an action, for display.
func RequiredRole(action Action) string { return required[action] }

// LegacyFallback is the role a user holds while per-project enforcement is off.
//
// Existing installs have no project memberships at all -- the only way to get one was a
// hard-coded "owner" -- so switching enforcement on without this would lock every non-admin
// out of every project at once. While off, the global role maps onto the new ladder the way
// it behaved before: a developer could change anything anywhere, a viewer could look.
func LegacyFallback(globalRole string) string {
	switch strings.ToLower(strings.TrimSpace(globalRole)) {
	case RoleAdmin:
		return RoleAdmin
	case RoleDeveloper:
		return RoleMaintainer
	case RoleViewer:
		return RoleViewer
	}
	return RoleNone
}
