package controllers

import (
	"os"
	"strings"
	"testing"
)

// config.registry.globalImagePullSecrets was settable in the chart, carried into VestaConfig,
// and read by nothing. GetGlobalImagePullSecrets had no callers at all, so a value set there
// did nothing — and the only symptom was ImagePullBackOff on an app whose registry had, as
// far as anyone configuring it could tell, been set up.
//
// Read from source because the alternative is a fake ConfigResolver and a fake client, which
// would test the scaffolding rather than whether the call happens.
func TestGlobalPullSecretsAreUsed(t *testing.T) {
	raw, err := os.ReadFile("vestaapp_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	if n := strings.Count(src, "GetGlobalImagePullSecrets()"); n < 2 {
		t.Errorf("GetGlobalImagePullSecrets is called %d times, want both the ServiceAccount "+
			"list and the pod spec: a secret on one but not the other either cannot be "+
			"copied into the namespace or is copied and never referenced", n)
	}

	// Broadest first. The order decides nothing functionally, since names are deduplicated,
	// but a narrower scope listed first would read as if it took precedence.
	for _, fn := range []string{"addRef", "addPullSecret"} {
		global := strings.Index(src, fn+"(r.ConfigResolver.GetGlobalImagePullSecrets())")
		project := strings.Index(src, fn+"(projectPullSecrets)")
		if global < 0 || project < 0 {
			t.Errorf("%s: could not find both the global and project calls", fn)
			continue
		}
		if global > project {
			t.Errorf("%s: the global secrets are added after the project's", fn)
		}
	}
}

// A nil resolver must not panic. The reconciler's zero value is used in tests, and a nil
// check costs nothing against a crash loop in the operator.
func TestGlobalPullSecretsTolerateNoResolver(t *testing.T) {
	raw, err := os.ReadFile("vestaapp_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	if strings.Count(src, "if r.ConfigResolver != nil {") < 2 {
		t.Error("GetGlobalImagePullSecrets is called without checking the resolver is set; " +
			"a nil one would panic the reconcile rather than simply having no globals")
	}
}
