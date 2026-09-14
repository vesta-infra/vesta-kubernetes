package activator

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The activator's matcher and the operator's default path list have to agree, and nothing
// connects them: the operator stamps an Ingress annotation, the activator parses it back,
// and the two packages share no types on purpose -- the activator has no CRD dependency at
// all, which is what lets it run without the operator's scheme.
//
// So this reads the default list out of the operator's source rather than importing it, and
// runs the real matcher over every entry. A path that the operator excludes but the matcher
// does not recognise would mean health checks keep waking the app, with nothing failing.
func TestTheMatcherRecognisesEveryDefaultPath(t *testing.T) {
	src, err := os.ReadFile("../api/v1alpha1/types.go")
	if err != nil {
		t.Skipf("operator types not available: %v", err)
	}

	block := regexp.MustCompile(`(?s)var DefaultNoWakePaths = \[\]string\{(.*?)\}`).FindSubmatch(src)
	if block == nil {
		t.Fatal("could not find DefaultNoWakePaths in the operator types; if it was renamed, " +
			"this guard is no longer checking anything")
	}

	paths := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(string(block[1]), -1)
	if len(paths) == 0 {
		t.Fatal("parsed no paths out of DefaultNoWakePaths")
	}

	var list []string
	for _, m := range paths {
		list = append(list, m[1])
	}

	for _, path := range list {
		if !matchesNoWake(path, list) {
			t.Errorf("the operator excludes %q by default, but the activator's matcher does "+
				"not recognise it -- a health check on that path would still wake the app", path)
		}
	}

	// The other half: ordinary traffic must still get through, or nothing ever wakes.
	for _, path := range []string{"/", "/api/orders", "/healthzz", "/health/details"} {
		if matchesNoWake(path, list) {
			t.Errorf("%q was treated as a health check; a request the app should serve would "+
				"get a stub from the activator instead", path)
		}
	}

	// And the list itself has to be a list of paths, not a single comma-joined string --
	// splitPaths is what the activator runs on the annotation, so a mismatch in separator
	// would silently produce one unmatchable entry.
	joined := strings.Join(list, ",")
	if got := splitPaths(joined); len(got) != len(list) {
		t.Errorf("splitPaths round-trip produced %d entries from %d", len(got), len(list))
	}
}
