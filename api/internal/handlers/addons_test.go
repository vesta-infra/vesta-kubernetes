package handlers

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The API validates add-on types against its own list, and the operator renders from its
// engine table. They are in different Go modules, so neither can import the other and the
// list is genuinely duplicated.
//
// Both are constrained by the CRD's enum, which is on disk and shared, so that is what this
// compares against. Without it the two drift silently in the worst direction: the API accepts
// a type the operator cannot render, and the add-on is created, stored, and then sits
// unready forever with a reason nobody is watching.
func TestSupportedAddonTypesMatchTheCRD(t *testing.T) {
	path := "../../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaaddons.yaml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}

	enum := enumAfter(string(raw), "type:")
	if len(enum) == 0 {
		t.Fatalf("no enum found for spec.type in %s -- the schema changed shape and this "+
			"test is now checking nothing", path)
	}

	got := append([]string(nil), supportedAddonTypes...)
	sort.Strings(got)
	sort.Strings(enum)

	if strings.Join(got, ",") != strings.Join(enum, ",") {
		t.Errorf("supportedAddonTypes = %v but the CRD allows %v.\n"+
			"A type the API accepts and the operator cannot render produces an add-on that "+
			"is created and then never becomes ready.", got, enum)
	}
}

// enumAfter finds the first `enum:` list following a line matching key, and returns its
// values. Hand-parsed rather than unmarshalled, matching podsizes_test.go: the point is to
// read what is actually on disk.
func enumAfter(src, key string) []string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != key {
			continue
		}
		var values []string
		inEnum := false
		for j := i + 1; j < len(lines) && j < i+20; j++ {
			trimmed := strings.TrimSpace(lines[j])
			switch {
			case trimmed == "enum:":
				inEnum = true
			case inEnum && strings.HasPrefix(trimmed, "- "):
				values = append(values, strings.TrimPrefix(trimmed, "- "))
			case inEnum:
				return values
			}
		}
		if len(values) > 0 {
			return values
		}
	}
	return nil
}

// The Secret name has to agree with the operator's, or binding an add-on injects a Secret
// that does not exist -- which surfaces as the app failing to start with a message about a
// missing secret rather than about the add-on.
func TestAddonSecretNameMatchesTheOperator(t *testing.T) {
	if got := addonSecretName("db"); got != "db-credentials" {
		t.Errorf("addonSecretName(db) = %q, want db-credentials (see AddonSecretName in the operator)", got)
	}
}

func TestUnsupportedTypesAreRejected(t *testing.T) {
	for _, ok := range supportedAddonTypes {
		if !isSupportedAddonType(ok) {
			t.Errorf("isSupportedAddonType(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "cassandra", "Postgres", "postgres ", "sqlite"} {
		if isSupportedAddonType(bad) {
			t.Errorf("isSupportedAddonType(%q) = true", bad)
		}
	}
}

func TestAddonPodTarget(t *testing.T) {
	addon := func(project, env string) *unstructured.Unstructured {
		spec := map[string]interface{}{"type": "postgres", "project": project}
		if env != "" {
			spec["environment"] = env
		}
		u := &unstructured.Unstructured{Object: map[string]interface{}{"spec": spec}}
		u.SetName("db")
		u.SetLabels(map[string]string{"kubernetes.getvesta.sh/project": project})
		return u
	}

	cases := []struct {
		name          string
		addon         *unstructured.Unstructured
		project, env  string
		wantStatus    int
		wantNamespace string
	}{
		{"all environments", addon("shop", ""), "shop", "staging", http.StatusOK, "shop-staging"},
		{"pinned, matching", addon("shop", "prod"), "shop", "prod", http.StatusOK, "shop-prod"},
		{"pinned, other environment", addon("shop", "prod"), "shop", "staging", http.StatusBadRequest, ""},
		{"another project's add-on", addon("other", ""), "shop", "staging", http.StatusNotFound, ""},
		{"missing add-on", nil, "shop", "staging", http.StatusNotFound, ""},
		{"no environment", addon("shop", ""), "shop", "", http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, sel, status, _ := addonPodTarget(tc.addon, tc.project, tc.env)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if status != http.StatusOK {
				return
			}
			if ns != tc.wantNamespace {
				t.Errorf("namespace = %q, want %q", ns, tc.wantNamespace)
			}
			// Must match the label the operator puts on add-on pods (addon_render.go).
			if sel != "kubernetes.getvesta.sh/addon=db" {
				t.Errorf("selector = %q", sel)
			}
		})
	}
}
