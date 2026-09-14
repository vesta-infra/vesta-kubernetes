package controllers

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// A field the code writes but the shipped CRD does not declare is discarded by the API
// server, silently and with no error on the write path.
//
// That is not hypothetical. spec.git carried commitSHA and tokenSecret in Go code, in the
// API handlers and in the UI form for a long time while the CRD declared neither, and the
// schema is structural with no preserved unknown fields -- so both were pruned on every
// write. The visible symptoms were a deploy-on-push path that did nothing for pre-built
// images and a Token Secret input that stored nothing. Nothing logged, nothing failed.
//
// `make sync-crds` copies only properties the shipped schema is MISSING, so it does add new
// fields -- but only if somebody runs it. This test is the thing that notices when nobody
// did.
func TestShippedCRDDeclaresEveryGitSourceField(t *testing.T) {
	props := chartProperties(t,
		"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml",
		"spec", "git")

	assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.GitSource{}), props, "spec.git")
}

// The same check for VestaAddonSpec. A field the API writes and the CRD does not declare is
// pruned on the way in, so the add-on is created with that setting silently absent.
func TestShippedCRDDeclaresEveryAddonField(t *testing.T) {
	props := chartProperties(t,
		"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaaddons.yaml", "spec")

	assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.VestaAddonSpec{}), props, "spec")
}

// The sleep policy, for the same reason. These fields are the difference between an app
// that sleeps by itself and one that never does, and a pruned field fails in the quiet
// direction: the UI shows the box ticked, the sweeper reads nothing, and nothing happens.
func TestShippedCRDDeclaresEverySleepField(t *testing.T) {
	props := chartProperties(t,
		"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml",
		"spec", "sleep")

	assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.SleepConfig{}), props, "spec.sleep")
}

// The quota spec, which appears in three CRDs and drifts independently in each. A pruned
// field here is a quota dimension that is configured, displayed, and silently not applied.
func TestShippedCRDsDeclareEveryQuotaField(t *testing.T) {
	for _, tc := range []struct {
		file string
		path []string
	}{
		{"kubernetes.getvesta.sh_vestaenvironments.yaml", []string{"spec", "quota"}},
		{"kubernetes.getvesta.sh_vestaprojects.yaml", []string{"spec", "quota"}},
		{"kubernetes.getvesta.sh_vestaconfigs.yaml", []string{"spec", "quotaDefaults"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			props := chartProperties(t, "../../deploy/helm/vesta/crds/"+tc.file, tc.path...)
			assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.QuotaSpec{}), props,
				strings.Join(tc.path, "."))
		})
	}
}

// VestaProjectSpec.DefaultGit reuses GitSource, so it is the same schema in a second file
// and drifts independently -- sync-crds writes both, a hand-edit usually writes one.
func TestShippedProjectCRDDeclaresEveryGitSourceField(t *testing.T) {
	props := chartProperties(t,
		"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaprojects.yaml",
		"spec", "defaultGit")

	assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.GitSource{}), props, "spec.defaultGit")
}

func assertStructFieldsDeclared(t *testing.T, typ reflect.Type, props map[string]interface{}, path string) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		name := jsonFieldName(typ.Field(i))
		if name == "" {
			continue
		}
		if _, ok := props[name]; !ok {
			t.Errorf("%s.%s exists on %s but not in the shipped CRD.\n"+
				"The API server will silently prune it on every write. Run `make generate && "+
				"make sync-crds`, and check the result -- sync-crds only adds properties the "+
				"schema is missing, it never updates one that is already there.",
				path, name, typ.Name())
		}
	}
}

func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	if i := strings.Index(tag, ","); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

// chartProperties walks a CRD's openAPIV3Schema down the given property path and returns
// the properties map at the end of it.
func chartProperties(t *testing.T, path string, keys ...string) map[string]interface{} {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}

	var doc struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema map[string]interface{} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Spec.Versions) == 0 {
		t.Fatalf("%s declares no versions", path)
	}

	node := doc.Spec.Versions[0].Schema.OpenAPIV3Schema
	for _, key := range keys {
		props, ok := node["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: no properties above %q", path, key)
		}
		next, ok := props[key].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: no property %q -- the schema was restructured and this test is "+
				"now checking nothing", path, key)
		}
		node = next
	}

	props, ok := node["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s: %v has no properties", path, keys)
	}
	return props
}

// A pruned security field is not a cosmetic bug: a hardening profile the API server drops
// leaves the operator resolving the default, so an instance configured as "restricted" runs
// exactly as unhardened as before and nothing anywhere says so.
func TestShippedCRDsDeclareEverySecurityField(t *testing.T) {
	t.Run("config.security", func(t *testing.T) {
		props := chartProperties(t,
			"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaconfigs.yaml",
			"spec", "security")
		assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.SecurityConfig{}), props, "spec.security")
	})

	t.Run("config.security.networkIsolation", func(t *testing.T) {
		props := chartProperties(t,
			"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaconfigs.yaml",
			"spec", "security", "networkIsolation")
		assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.NetworkIsolationConfig{}), props,
			"spec.security.networkIsolation")
	})

	t.Run("environment.status.networkIsolation", func(t *testing.T) {
		props := chartProperties(t,
			"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaenvironments.yaml",
			"status", "networkIsolation")
		assertStructFieldsDeclared(t, reflect.TypeOf(vestav1alpha1.NetworkIsolationStatus{}), props,
			"status.networkIsolation")
	})

	// The per-app override, which is what makes "restricted" usable at all.
	t.Run("app.securityProfile", func(t *testing.T) {
		props := chartProperties(t,
			"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml", "spec")
		if _, ok := props["securityProfile"]; !ok {
			t.Error("spec.securityProfile is not declared in the shipped vestaapps CRD; " +
				"a per-app profile would be pruned and every app would silently inherit the platform default")
		}
	})
}

// The enum has to accept every profile the Go code resolves, or configuring one is rejected
// at admission -- and merge-crd-properties.py never updates an existing property's enum, so
// this is exactly the drift that goes unnoticed.
func TestSecurityProfileEnumsAcceptEveryProfile(t *testing.T) {
	want := []string{ProfileLegacy, ProfileBaseline, ProfileRestricted}

	for _, tc := range []struct {
		file string
		path []string
	}{
		{"kubernetes.getvesta.sh_vestaapps.yaml", []string{"spec", "securityProfile"}},
		{"kubernetes.getvesta.sh_vestaconfigs.yaml", []string{"spec", "security", "profile"}},
	} {
		t.Run(strings.Join(tc.path, "."), func(t *testing.T) {
			parent := chartProperties(t, "../../deploy/helm/vesta/crds/"+tc.file, tc.path[:len(tc.path)-1]...)
			field, ok := parent[tc.path[len(tc.path)-1]].(map[string]interface{})
			if !ok {
				t.Fatalf("%s is not declared", strings.Join(tc.path, "."))
			}

			raw, ok := field["enum"].([]interface{})
			if !ok {
				t.Fatalf("%s declares no enum, so any string is accepted", strings.Join(tc.path, "."))
			}
			got := map[string]bool{}
			for _, v := range raw {
				got[v.(string)] = true
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("the shipped enum for %s rejects %q, which the operator resolves to",
						strings.Join(tc.path, "."), w)
				}
			}
			// Negative control: a value the operator does not know must not be accepted.
			if got["permissive"] {
				t.Error("the enum accepts a profile the operator does not implement")
			}
		})
	}
}

// A pruned scope field is an access-control failure that looks like nothing.
//
// If spec.scope does not survive a write, every credential reads back unscoped -- and
// unscoped resolves to global, by design, so a credential someone deliberately restricted to
// one project would be silently usable instance-wide. The API would report success, the UI
// would show the scope the user picked until the next refresh, and nothing would be wrong
// anywhere except in who can reach the secret.
func TestShippedCRDsDeclareSecretScope(t *testing.T) {
	props := chartProperties(t,
		"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestasecrets.yaml", "spec")

	for _, field := range []string{"scope", "project"} {
		if _, ok := props[field]; !ok {
			t.Errorf("spec.%s is not declared in the shipped vestasecrets CRD; it would be "+
				"pruned on write and every credential would read back as global", field)
		}
	}

	scope, ok := props["scope"].(map[string]interface{})
	if !ok {
		t.Fatal("spec.scope is not an object")
	}
	enum, ok := scope["enum"].([]interface{})
	if !ok {
		t.Fatal("spec.scope declares no enum, so any string is accepted -- and anything " +
			"unrecognised resolves to global")
	}
	got := map[string]bool{}
	for _, v := range enum {
		got[v.(string)] = true
	}
	for _, want := range []string{"global", "project"} {
		if !got[want] {
			t.Errorf("the shipped enum for spec.scope rejects %q", want)
		}
	}

	// And the platform default that feeds it.
	cfg := chartProperties(t,
		"../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaconfigs.yaml", "spec", "security")
	if _, ok := cfg["defaultSecretScope"]; !ok {
		t.Error("spec.security.defaultSecretScope is not declared in the shipped vestaconfigs " +
			"CRD; setting it would be pruned and the instance would stay on global")
	}
}
