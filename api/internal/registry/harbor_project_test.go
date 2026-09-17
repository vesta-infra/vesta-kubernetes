package registry

import "testing"

// A build against OVH's registry failed at the push, after the image had been built:
//
//	BAD_REQUEST: invalid repository name: atom-website
//
// OVH's container registry is Harbor, where every repository lives under a project, so
// "<host>/atom-website" is not a name it can accept. The path there was typed by hand
// because the repository picker had nothing to offer: read as generic v2, listing goes to
// /v2/_catalog, which Harbor restricts to administrators.
func TestHostedHarborIsDetected(t *testing.T) {
	cases := map[string]string{
		"hev12uhv.eu-west-par.container-registry.ovh.net": FlavorHarbor,
		"https://abc.gra7.container-registry.ovh.net":     FlavorHarbor,
		"harbor.example.com":                              FlavorHarbor,
		"registry.example.com":                            FlavorGenericV2,
		"ghcr.io":                                         FlavorGHCR,
		"docker.io":                                       FlavorDockerHub,
	}
	for host, want := range cases {
		if got := DetectFlavor(host); got != want {
			t.Errorf("DetectFlavor(%q) = %q, want %q", host, got, want)
		}
	}
}

// Only Harbor demands a project. Saying so of a plain v2 registry would reject
// "registry.example.com/myapp", which is a perfectly ordinary name there.
func TestOnlyHarborNeedsAProjectPath(t *testing.T) {
	if !RequiresProjectPath(FlavorHarbor) {
		t.Error("Harbor does require a project path")
	}
	for _, flavor := range []string{FlavorGenericV2, FlavorGHCR, FlavorDockerHub} {
		if RequiresProjectPath(flavor) {
			t.Errorf("%s does not require a project path; a single-segment repository is "+
				"valid there", flavor)
		}
	}
}

// The name from the failure, parsed: one segment after the host is what Harbor rejects.
func TestSingleSegmentPathIsVisible(t *testing.T) {
	ref, err := ParseImageRef("hev12uhv.eu-west-par.container-registry.ovh.net/atom-website:build-1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Path != "atom-website" {
		t.Fatalf("path = %q", ref.Path)
	}
	if RequiresProjectPath(DetectFlavor(ref.Host)) && !containsSlash(ref.Path) {
		return // the combination this is meant to make visible
	}
	t.Error("a single-segment path on a Harbor host was not recognisable as a problem")
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
