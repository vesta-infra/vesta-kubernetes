package controllers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// NeedsPasswordMigration guards a destructive operation: every case where it wrongly says
// yes is a case where a password gets cleared. A registry password exists nowhere else --
// not in the cluster, not in a backup Vesta controls -- so clearing one that was not safely
// copied first loses it, and the only symptom is an ImagePullBackOff nobody can fix without
// knowing the original.
func TestNothingIsMigratedThatShouldNotBe(t *testing.T) {
	cases := []struct {
		name string
		dc   *vestav1alpha1.DockerSecretConfig
		want bool
	}{
		{"no docker config at all", nil, false},
		{"no password to move", &vestav1alpha1.DockerSecretConfig{Registry: "r", Username: "u"}, false},

		// Already migrated: the ref is set and the plaintext is gone. Touching this again
		// would be a pointless write on every single reconcile.
		{"already migrated", &vestav1alpha1.DockerSecretConfig{
			PasswordSecretRef: &vestav1alpha1.DrainSecretRef{Name: "vesta-registry-x", Key: "password"},
		}, false},

		// The real case.
		{"plaintext to move", &vestav1alpha1.DockerSecretConfig{Password: "hunter2"}, true},

		// A half-finished migration: the ref was set but the plaintext survived, because the
		// clearing patch failed. Finishing it is correct -- but only after the Secret is
		// verified, which is the caller's job.
		{"interrupted migration", &vestav1alpha1.DockerSecretConfig{
			Password:          "hunter2",
			PasswordSecretRef: &vestav1alpha1.DrainSecretRef{Name: "vesta-registry-x"},
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsPasswordMigration(tc.dc); got != tc.want {
				t.Errorf("NeedsPasswordMigration = %v, want %v", got, tc.want)
			}
		})
	}
}

// The name has to be derived from the VestaSecret's own name, or the mapping needs a lookup
// table that can go stale.
func TestPasswordSecretNameIsDerived(t *testing.T) {
	got := registryPasswordSecretName("harbor-prod")
	if !strings.Contains(got, "harbor-prod") {
		t.Errorf("= %q, want it to contain the credential's name", got)
	}
	if got == "harbor-prod" {
		t.Error("the password Secret shares a name with something else in the namespace")
	}
	// Two credentials must not collide onto one Secret.
	if registryPasswordSecretName("a") == registryPasswordSecretName("b") {
		t.Error("two credentials map to the same password Secret")
	}
}

// The docker config the kubelet reads has to carry the resolved password, not whatever is
// left in the spec. Once migrated, spec.dockerConfig.password is empty -- and a builder that
// read from there would produce a config with an empty password and every pull would fail
// with an authentication error rather than anything naming the cause.
func TestDockerConfigUsesTheResolvedPassword(t *testing.T) {
	dc := &vestav1alpha1.DockerSecretConfig{
		Registry: "harbor.example.com",
		Username: "robot$ci",
		Password: "", // migrated away
		PasswordSecretRef: &vestav1alpha1.DrainSecretRef{
			Name: "vesta-registry-harbor", Key: "password",
		},
	}

	raw, err := buildDockerConfigJSON(dc, "resolved-from-secret")
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}

	entry, ok := parsed.Auths["harbor.example.com"]
	if !ok {
		t.Fatalf("no auths entry for the registry: %s", raw)
	}
	if entry.Password != "resolved-from-secret" {
		t.Errorf("password = %q, want the resolved value", entry.Password)
	}
	// The base64 auth field is what most registries actually read, so an empty password
	// there fails even when the password field looks right.
	if !strings.Contains(string(mustDecode(t, entry.Auth)), "resolved-from-secret") {
		t.Errorf("the auth field does not carry the resolved password: %q", entry.Auth)
	}
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding auth: %v", err)
	}
	return out
}
