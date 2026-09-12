package handlers

import (
	"os"
	"strings"
	"testing"
)

// A registry with a port puts a colon in the host, so splitting on the last colon alone
// reports "5000/vesta/api" as the tag.
func TestImageTagHandlesRegistryPort(t *testing.T) {
	for _, c := range []struct{ image, want string }{
		{"ghcr.io/vesta-infra/kubernetes-api:0.6.3", "0.6.3"},
		{"registry.local:5000/vesta/api:0.6.3", "0.6.3"},
		{"registry.local:5000/vesta/api", "latest"},
		{"vesta/api", "latest"},
		{"", "unknown"},
	} {
		if got := imageTag(c.image); got != c.want {
			t.Errorf("imageTag(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}

// The version reaches an image tag and a helm --version, so anything that is not an
// immutable release identifier has to be refused before it gets near either. "latest" in
// particular would be a silent no-op on nodes that cached it, since pullPolicy is
// IfNotPresent.
func TestSemverPatternRejectsMovingTags(t *testing.T) {
	for _, bad := range []string{"latest", "main", "develop", "v1.2.3", "1.2", "1.2.3.4", "", "../../etc", "1.2.3 && rm -rf /"} {
		if semverPattern.MatchString(bad) {
			t.Errorf("%q was accepted as a release version", bad)
		}
	}
}

func TestSemverPatternAcceptsReleases(t *testing.T) {
	for _, good := range []string{"0.6.3", "1.0.0", "10.20.30", "1.2.3-rc.1"} {
		if !semverPattern.MatchString(good) {
			t.Errorf("%q was rejected as a release version", good)
		}
	}
}

// The API cannot otherwise learn where it lives, and guessing "vesta-system" on an
// install that used another namespace would make it manage nothing at all.
func TestReleaseNamespacePrefersEnvironment(t *testing.T) {
	t.Setenv("VESTA_NAMESPACE", "platform")
	if got := ReleaseNamespace(); got != "platform" {
		t.Errorf("ReleaseNamespace() = %q, want the configured namespace", got)
	}
}

func TestReleaseNamespaceFallsBackToConvention(t *testing.T) {
	t.Setenv("VESTA_NAMESPACE", "")
	if got := ReleaseNamespace(); got != vestaSystemNS {
		t.Errorf("ReleaseNamespace() = %q, want %q", got, vestaSystemNS)
	}
}

// Deployment names are "<release>-api", so anything that hardcodes "vesta-api" breaks for
// an install created with `helm install myvesta`.
func TestComponentDiscoveryIsByLabelNotName(t *testing.T) {
	source, err := os.ReadFile("system.go")
	if err != nil {
		t.Fatalf("reading system.go: %v", err)
	}
	body, ok := functionBody(string(source), "func (h *Handler) observedComponents(")
	if !ok {
		t.Fatal("observedComponents not found")
	}
	if !strings.Contains(body, "app.kubernetes.io/part-of=vesta") {
		t.Error("components must be discovered by label")
	}
	if strings.Contains(body, `"vesta-api"`) || strings.Contains(body, `"-api"`) {
		t.Error("component discovery must not construct deployment names")
	}
}
