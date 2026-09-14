package handlers

import (
	"os"
	"sort"
	"strings"
	"testing"
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
