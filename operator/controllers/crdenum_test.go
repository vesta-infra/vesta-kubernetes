package controllers

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The chart's CRDs are not generated from the Go types -- hack/merge-crd-properties.py
// copies across only properties the shipped schema is *missing*, and recurses into ones it
// already has. It never updates an existing property's scalar attributes, so widening an
// enum in types.go does not reach deploy/helm/vesta/crds/ no matter how many times you run
// `make generate && make sync-crds`.
//
// That asymmetry is quiet in one direction and loud in the other. A property the shipped
// schema lacks is pruned silently on write. An enum value it lacks is a 422 -- so an
// operator writing a phase the chart has never heard of fails its status update on every
// reconcile, forever, while the CRD sitting in the repo looks correct.
//
// Adding "Stopped" hit exactly this. The test compares the two directly rather than
// trusting the sync step.
func TestShippedCRDAcceptsEveryPhaseTheOperatorWrites(t *testing.T) {
	goEnum := kubebuilderEnum(t, "../api/v1alpha1/types.go", "Phase string `json:\"phase,omitempty\"`")
	chartEnum := chartPhaseEnum(t, "../../deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml")

	if len(goEnum) == 0 || len(chartEnum) == 0 {
		t.Fatalf("parsed an empty enum: go=%v chart=%v", goEnum, chartEnum)
	}

	shipped := make(map[string]bool, len(chartEnum))
	for _, v := range chartEnum {
		shipped[v] = true
	}
	for _, v := range goEnum {
		if !shipped[v] {
			t.Errorf("status.phase value %q is legal in types.go but absent from the chart's CRD.\n"+
				"The API server will reject every status write carrying it. `make sync-crds` will "+
				"not fix this -- add it by hand to deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml.",
				v)
		}
	}
}

// kubebuilderEnum finds the field whose declaration matches decl and returns the values of
// the +kubebuilder:validation:Enum marker in the comment block directly above it.
func kubebuilderEnum(t *testing.T, path, decl string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}

	lines := strings.Split(string(src), "\n")
	field := -1
	for i, line := range lines {
		if strings.Contains(line, decl) {
			field = i
			break
		}
	}
	if field < 0 {
		t.Fatalf("no field matching %q in %s -- the declaration was renamed and this test "+
			"is now checking nothing", decl, path)
	}

	marker := regexp.MustCompile(`\+kubebuilder:validation:Enum=(.+)$`)
	for i := field - 1; i >= 0 && i > field-25; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "//") {
			break
		}
		if m := marker.FindStringSubmatch(line); m != nil {
			return strings.Split(strings.TrimSpace(m[1]), ";")
		}
	}
	t.Fatalf("no +kubebuilder:validation:Enum marker above %q in %s", decl, path)
	return nil
}

// chartPhaseEnum pulls the status.phase enum out of the shipped CRD. Hand-parsed rather
// than unmarshalled, matching podsizes_test.go: the point is to read what is on disk
// without a dependency that might normalise it on the way in.
func chartPhaseEnum(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}

	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "phase:" {
			continue
		}
		var values []string
		inEnum := false
		for j := i + 1; j < len(lines); j++ {
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
		return values
	}
	t.Fatalf("no phase field in %s -- the status schema was restructured and this test is "+
		"now checking nothing", path)
	return nil
}
