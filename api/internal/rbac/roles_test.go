package rbac

import "testing"

// This table is the feature. Everything else in the RBAC work is plumbing that routes a
// request to one of these answers.
func TestEffectiveRole(t *testing.T) {
	cases := []struct {
		name                 string
		global, project, env string
		want                 string
	}{
		// A global admin must win everywhere. Without this an admin could remove their own
		// project membership and have no way back in.
		{"admin beats everything", RoleAdmin, RoleViewer, RoleViewer, RoleAdmin},
		{"admin with no memberships", RoleAdmin, RoleNone, RoleNone, RoleAdmin},

		{"project role applies when no env role", RoleDeveloper, RoleMaintainer, RoleNone, RoleMaintainer},
		{"no memberships means nothing", RoleDeveloper, RoleNone, RoleNone, RoleNone},

		// The case the whole feature exists for.
		{"env raises the project role", RoleDeveloper, RoleViewer, RoleDeployer, RoleDeployer},
		// And its mirror, which is the one people actually ask for: broad access to the
		// project, read-only on production.
		{"env lowers the project role", RoleDeveloper, RoleMaintainer, RoleViewer, RoleViewer},

		{"case and whitespace are tolerated", RoleDeveloper, " Owner ", "", RoleOwner},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveRole(tc.global, tc.project, tc.env); got != tc.want {
				t.Errorf("EffectiveRole(%q, %q, %q) = %q, want %q",
					tc.global, tc.project, tc.env, got, tc.want)
			}
		})
	}
}

// The capability matrix, stated once. If this table is wrong the feature is wrong, however
// correct the middleware around it is.
func TestCan(t *testing.T) {
	allowed := map[string]map[Action]bool{
		RoleNone: {
			ActionRead: false, ActionDeploy: false, ActionWrite: false,
			ActionSecrets: false, ActionAdmin: false,
		},
		RoleViewer: {
			ActionRead: true, ActionDeploy: false, ActionWrite: false,
			ActionSecrets: false, ActionAdmin: false,
		},
		RoleDeployer: {
			ActionRead: true, ActionDeploy: true, ActionWrite: false,
			ActionSecrets: false, ActionAdmin: false,
		},
		RoleMaintainer: {
			ActionRead: true, ActionDeploy: true, ActionWrite: true,
			ActionSecrets: true, ActionAdmin: false,
		},
		RoleOwner: {
			ActionRead: true, ActionDeploy: true, ActionWrite: true,
			ActionSecrets: true, ActionAdmin: true,
		},
		RoleAdmin: {
			ActionRead: true, ActionDeploy: true, ActionWrite: true,
			ActionSecrets: true, ActionAdmin: true,
		},
	}

	for role, actions := range allowed {
		for action, want := range actions {
			if got := Can(role, action); got != want {
				t.Errorf("Can(%q, %q) = %v, want %v", role, action, got, want)
			}
		}
	}
}

// A deployer must not reach secrets. Deploying code and reading credentials are different
// powers, and exec and file access are the same power as reading a secret by another route.
func TestDeployerCannotReachSecrets(t *testing.T) {
	if Can(RoleDeployer, ActionSecrets) {
		t.Error("a deployer can read secrets; exec and file access grant the same thing, so this level must sit above deploy")
	}
	if !Can(RoleDeployer, ActionDeploy) {
		t.Error("a deployer cannot deploy")
	}
}

// An unrecognised role must reduce access, never grant it. A typo in a role column is far
// more likely than a deliberate new role, and the safe reading of one is "nobody".
func TestUnknownRolesGrantNothing(t *testing.T) {
	for _, role := range []string{"superuser", "Owner!", "admin ", "root", "0", "true"} {
		for _, action := range []Action{ActionRead, ActionDeploy, ActionWrite, ActionSecrets, ActionAdmin} {
			if role == "admin " {
				continue // trimmed to a real role on purpose
			}
			if Can(role, action) {
				t.Errorf("unknown role %q was allowed to %q", role, action)
			}
		}
	}
}

// An action nobody has described must be refused, so a new endpoint is closed until somebody
// says what it needs rather than open until somebody notices.
func TestUnknownActionsAreRefused(t *testing.T) {
	for _, role := range []string{RoleOwner, RoleAdmin, RoleMaintainer} {
		if Can(role, Action("teleport")) {
			t.Errorf("role %q was allowed an undescribed action", role)
		}
	}
}

// Only project-scoped roles may be stored against a project. A global role recorded there
// would read as more powerful than owner and rank above it.
func TestValidRejectsGlobalRoles(t *testing.T) {
	for _, role := range []string{RoleViewer, RoleDeployer, RoleMaintainer, RoleOwner} {
		if !Valid(role) {
			t.Errorf("Valid(%q) = false, want true", role)
		}
	}
	for _, role := range []string{RoleAdmin, RoleDeveloper, "", "superuser"} {
		if Valid(role) {
			t.Errorf("Valid(%q) = true, want false", role)
		}
	}
}

// Existing installs have no project memberships, because the only way to get one was a
// hard-coded "owner". Turning enforcement on without this locks every non-admin out of every
// project at once.
func TestLegacyFallbackPreservesTodaysBehaviour(t *testing.T) {
	cases := map[string]string{
		RoleAdmin:     RoleAdmin,
		RoleDeveloper: RoleMaintainer,
		RoleViewer:    RoleViewer,
		"":            RoleNone,
		"nonsense":    RoleNone,
	}
	for global, want := range cases {
		if got := LegacyFallback(global); got != want {
			t.Errorf("LegacyFallback(%q) = %q, want %q", global, got, want)
		}
	}

	// The behaviour being preserved: a developer could change any app anywhere, and could
	// read secrets, but was never able to manage access.
	dev := LegacyFallback(RoleDeveloper)
	if !Can(dev, ActionWrite) || !Can(dev, ActionSecrets) {
		t.Error("a legacy developer lost abilities they had before enforcement existed")
	}
	if Can(dev, ActionAdmin) {
		t.Error("a legacy developer gained the ability to manage access")
	}

	// And a viewer could look and nothing else.
	viewer := LegacyFallback(RoleViewer)
	if !Can(viewer, ActionRead) || Can(viewer, ActionDeploy) {
		t.Error("a legacy viewer's abilities changed")
	}
}

func TestAllowsIsAHierarchy(t *testing.T) {
	ladder := []string{RoleViewer, RoleDeployer, RoleMaintainer, RoleOwner, RoleAdmin}
	for i, role := range ladder {
		for j, required := range ladder {
			want := i >= j
			if got := Allows(role, required); got != want {
				t.Errorf("Allows(%q, %q) = %v, want %v", role, required, got, want)
			}
		}
	}

	// Requiring nothing is not a way to allow everyone.
	if Allows(RoleOwner, RoleNone) {
		t.Error("Allows treated an empty requirement as satisfiable; that would make an ungated route look gated")
	}
}
