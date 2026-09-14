package services

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The CRD's build-strategy enum and what the builder implements are two different lists, and
// nothing connects them. This pins the difference so it is a recorded decision rather than a
// surprise during somebody's first deploy.
//
// The gap is real: "runpacks" is accepted at admission and then fails at build time. It is
// left accepted on purpose -- narrowing a CRD enum is a data migration, and an app already
// storing that value would become unappliable -- but a gap nobody has written down is the
// kind that gets "fixed" by deleting the enum value.
func TestBuildStrategyEnumAndBuilderAgree(t *testing.T) {
	src, err := os.ReadFile("../../../operator/api/v1alpha1/types.go")
	if err != nil {
		t.Skipf("operator source not available: %v", err)
	}

	m := regexp.MustCompile(`\+kubebuilder:validation:Enum=([a-z;]*dockerfile[a-z;]*)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("could not find the build-strategy enum in the operator types")
	}
	declared := strings.Split(string(m[1]), ";")

	supported := map[string]bool{}
	for _, s := range SupportedBuildStrategies {
		supported[s] = true
	}

	// Anything the CRD accepts is either buildable, or one of these two with a reason.
	knownGaps := map[string]string{
		"image":    "an app deploying a pre-built image never reaches a builder",
		"runpacks": "not implemented; accepted at admission and fails at build time",
	}

	for _, value := range declared {
		if supported[value] || knownGaps[value] != "" {
			continue
		}
		t.Errorf("the CRD accepts build strategy %q, which the builder does not implement "+
			"and which is not recorded as a known gap; it would be accepted on save and fail "+
			"during a deploy", value)
	}

	// And the reverse: a strategy the builder implements but the CRD rejects could never be
	// selected at all.
	for _, s := range SupportedBuildStrategies {
		var found bool
		for _, d := range declared {
			if d == s {
				found = true
			}
		}
		if !found {
			t.Errorf("the builder implements %q but the CRD enum rejects it, so it can never be set", s)
		}
	}
}

// The error a user sees when they hit the gap has to say what to do instead. "unsupported
// build strategy: runpacks" told them only that they were wrong.
func TestUnsupportedStrategyErrorNamesTheAlternatives(t *testing.T) {
	b := &Builder{}
	_, err := b.createBuildJob(BuildRequest{Strategy: "runpacks", Repository: "acme/web"}, "build-test")
	if err == nil {
		t.Fatal("an unimplemented strategy produced no error")
	}
	for _, want := range []string{"runpacks", "dockerfile", "nixpacks", "buildpacks"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
