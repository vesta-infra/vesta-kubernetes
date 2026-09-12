package handlers

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestHostPatternRejectsBadInput(t *testing.T) {
	for _, bad := range []string{
		"", "localhost", "no_underscores.example.com", "-leading.example.com",
		"trailing-.example.com", "has space.example.com", "UPPER.example.com",
		"http://example.com", "example.com/path", "..",
	} {
		if hostPattern.MatchString(bad) {
			t.Errorf("%q was accepted as a hostname", bad)
		}
	}
}

func TestHostPatternAcceptsRealHosts(t *testing.T) {
	for _, good := range []string{"k8.credpal.xyz", "a.b", "vesta-ui.example.co.uk", "x1.y2.z3"} {
		if !hostPattern.MatchString(good) {
			t.Errorf("%q was rejected as a hostname", good)
		}
	}
}

func uiIngressFixture(service, secret string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "myvesta-ui", "namespace": "vesta-system"},
		"spec": map[string]interface{}{
			"rules": []interface{}{map[string]interface{}{
				"host": "old.example.com",
				"http": map[string]interface{}{"paths": []interface{}{map[string]interface{}{
					"backend": map[string]interface{}{"service": map[string]interface{}{
						"name": service, "port": map[string]interface{}{"number": int64(80)}}}}}},
			}},
			"tls": []interface{}{map[string]interface{}{
				"hosts": []interface{}{"old.example.com"}, "secretName": secret}},
		},
	}}
}

// The service is read from the live object, not reconstructed from the ingress name.
// They match in the stock chart, which would hide a wrong assumption here.
func TestExistingBackendPrefersTheLiveService(t *testing.T) {
	svc, secret := existingBackend(uiIngressFixture("some-other-service", "custom-tls"))
	if svc != "some-other-service" {
		t.Errorf("service = %q, want the one on the object", svc)
	}
	if secret != "custom-tls" {
		t.Errorf("secret = %q, want the one on the object", secret)
	}
}

func TestExistingBackendFallsBackToChartNaming(t *testing.T) {
	bare := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "myvesta-ui"},
		"spec":     map[string]interface{}{},
	}}
	svc, secret := existingBackend(bare)
	if svc != "myvesta-ui" || secret != "myvesta-ui-tls" {
		t.Errorf("fallback = %q/%q, want myvesta-ui/myvesta-ui-tls", svc, secret)
	}
}

// PatchResource sends a JSON merge patch, which replaces arrays wholesale. A partial
// rules list would drop the backend and leave an ingress routing nowhere.
func TestPatchCarriesTheWholeBackend(t *testing.T) {
	p := buildUIIngressPatch("vesta-ui", "vesta-ui-tls",
		UIDomainSettings{Host: "new.example.com", TLS: true, ClusterIssuer: "letsencrypt-prod"})

	rules := p["spec"].(map[string]interface{})["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	if rule["host"] != "new.example.com" {
		t.Fatalf("host = %v", rule["host"])
	}
	path := rule["http"].(map[string]interface{})["paths"].([]interface{})[0].(map[string]interface{})
	svc := path["backend"].(map[string]interface{})["service"].(map[string]interface{})
	if svc["name"] != "vesta-ui" {
		t.Errorf("backend service = %v, want it preserved", svc["name"])
	}
	if path["pathType"] != "Prefix" || path["path"] != "/" {
		t.Errorf("path rule incomplete: %v", path)
	}
}

// Turning TLS off must remove the block, or cert-manager keeps ordering for a host that
// is no longer served. Merge patch needs an explicit null to delete a key.
func TestDisablingTLSNullsTheBlockAndIssuer(t *testing.T) {
	p := buildUIIngressPatch("vesta-ui", "vesta-ui-tls", UIDomainSettings{Host: "new.example.com", TLS: false})

	if tls, ok := p["spec"].(map[string]interface{})["tls"]; !ok || tls != nil {
		t.Errorf("spec.tls = %v, want an explicit nil", tls)
	}
	ann := p["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	if v, ok := ann["cert-manager.io/cluster-issuer"]; !ok || v != nil {
		t.Errorf("issuer annotation = %v, want an explicit nil", v)
	}
}

// The change is reverted by the next helm upgrade unless the same values are given to
// Helm, so the response has to hand them over rather than leave it implicit.
func TestHelmFlagsCoverWhatWasChanged(t *testing.T) {
	flags := strings.Join(helmFlagsFor(UIDomainSettings{
		Host: "k8.credpal.xyz", TLS: true, ClusterIssuer: "letsencrypt-prod", IngressClassName: "traefik",
	}), " ")
	for _, want := range []string{
		"ui.ingress.enabled=true", "ui.ingress.host=k8.credpal.xyz",
		"ui.ingress.tls=true", "ui.ingress.clusterIssuer=letsencrypt-prod",
		"ui.ingress.ingressClassName=traefik",
	} {
		if !strings.Contains(flags, want) {
			t.Errorf("flags missing %q: %s", want, flags)
		}
	}
}

func TestHelmFlagsOmitIssuerWhenTLSOff(t *testing.T) {
	flags := strings.Join(helmFlagsFor(UIDomainSettings{Host: "x.example.com", TLS: false, ClusterIssuer: "letsencrypt-prod"}), " ")
	if strings.Contains(flags, "clusterIssuer") {
		t.Errorf("issuer should not be offered with TLS off: %s", flags)
	}
}
