package handlers

import (
	"testing"

	"kubernetes.getvesta.sh/api/internal/rbac"
)

// Every registry credential that exists today was created before scoping did, so it carries
// no scope at all. If an empty scope resolved to anything but global, those credentials
// would become invisible to the people and apps already using them the moment this shipped --
// and the symptom would be ImagePullBackOff on deploys that worked yesterday.
func TestAnUnscopedSecretStaysGlobal(t *testing.T) {
	cases := []struct {
		name           string
		spec, platform string
	}{
		{"nothing set anywhere", "", ""},
		{"platform explicitly global", "", "global"},
		{"an unrecognised platform default", "", "nonsense"},
		{"an unrecognised scope on the secret", "nonsense", ""},
		// A typo in platform config must not hide credentials that apps depend on.
		{"a typo'd platform default", "", "Project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSecretScope(tc.spec, tc.platform); got != SecretScopeGlobal {
				t.Errorf("ResolveSecretScope(%q, %q) = %q, want global", tc.spec, tc.platform, got)
			}
		})
	}
}

// The platform default applies to secrets that name no scope, and a secret that names one
// wins over it -- so turning on project scoping instance-wide does not reclassify anything
// that already chose global.
func TestScopePrecedence(t *testing.T) {
	cases := []struct{ spec, platform, want string }{
		{"", "project", "project"},
		{"project", "", "project"},
		{"project", "global", "project"},
		{"global", "project", "global"},
		{"", "global", "global"},
	}
	for _, tc := range cases {
		if got := ResolveSecretScope(tc.spec, tc.platform); got != tc.want {
			t.Errorf("ResolveSecretScope(spec=%q, platform=%q) = %q, want %q",
				tc.spec, tc.platform, got, tc.want)
		}
	}
}

// What scoping is actually for.
func TestProjectScopedSecretsAreNotVisibleOutsideTheirProject(t *testing.T) {
	cases := []struct {
		name    string
		scope   string
		project string
		role    string
		action  rbac.Action
		want    bool
	}{
		// Global: allowed on the strength of the route's own gate, which is exactly what
		// every caller had before scoping existed.
		{"global, no role at all", "global", "", rbac.RoleNone, rbac.ActionRead, true},
		{"global with a project set", "global", "acme", rbac.RoleNone, rbac.ActionRead, true},

		// Project-scoped: membership decides.
		{"a member reading", "project", "acme", rbac.RoleMaintainer, rbac.ActionRead, true},
		{"a deployer reading", "project", "acme", rbac.RoleDeployer, rbac.ActionRead, true},
		{"a non-member reading", "project", "acme", rbac.RoleNone, rbac.ActionRead, false},
		{"a viewer reading", "project", "acme", rbac.RoleViewer, rbac.ActionRead, true},

		// Deleting takes more than reading: removing a credential an app pulls with breaks
		// that app's next deploy.
		{"a viewer deleting", "project", "acme", rbac.RoleViewer, rbac.ActionSecrets, false},
		{"an owner deleting", "project", "acme", rbac.RoleOwner, rbac.ActionSecrets, true},

		// Project-scoped but naming no project: nothing to check membership against, so
		// nobody can establish access. Fails closed rather than falling back to global.
		{"scoped to no project", "project", "", rbac.RoleOwner, rbac.ActionRead, false},
		{"scoped to no project, as admin", "project", "", rbac.RoleAdmin, rbac.ActionRead, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanAccessSecret(tc.scope, tc.project, tc.role, tc.action)
			if got != tc.want {
				t.Errorf("CanAccessSecret(%q, %q, %q, %q) = %v, want %v",
					tc.scope, tc.project, tc.role, tc.action, got, tc.want)
			}
		})
	}
}

// CanAccessSecret takes an EFFECTIVE project role -- a rung on the viewer/deployer/
// maintainer/owner ladder -- not a global role. "developer" is a global role that
// EffectiveRoleFor maps onto the ladder before this is ever called, so passing it here
// directly means a caller resolved the role wrongly.
//
// It must deny in that case. Granting on an unrecognised role would turn every such mistake
// into a silent authorisation bypass instead of a visible permission failure.
func TestAGlobalRolePassedAsALadderRoleIsDenied(t *testing.T) {
	if CanAccessSecret("project", "acme", rbac.RoleDeveloper, rbac.ActionRead) {
		t.Error("the global 'developer' role was accepted as a project role; an unrecognised " +
			"role must fail closed")
	}
}

// An admin is an admin everywhere. Scoping is about separating projects from each other, not
// about hiding things from the person who administers the instance.
func TestAdminsReachProjectScopedSecrets(t *testing.T) {
	if !CanAccessSecret("project", "acme", rbac.RoleAdmin, rbac.ActionSecrets) {
		t.Error("an admin could not reach a project-scoped credential")
	}
}
