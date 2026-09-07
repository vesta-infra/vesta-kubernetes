package handlers

import (
	"testing"
)

func ingress(fields map[string]interface{}) map[string]interface{} { return fields }

// The update path replaces spec.ingress wholesale. Any client that PUTs an ingress block
// without first reading the app back — the CLI, an API token, a CI integration — would
// otherwise silently drop the app's SSL provider and re-issue its certificate from the
// instance default.
func TestRestoreIngressCertFieldsPreservesOmittedSelection(t *testing.T) {
	stored := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"domain":        "app.example.com",
			"tls":           true,
			"clusterIssuer": "zerossl",
		}),
	}
	existing := collectIngressCertFields(stored)

	// A patch that only changes the domain.
	spec := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{"domain": "new.example.com", "tls": true}),
	}
	restoreIngressCertFields(spec, existing)

	got := spec["ingress"].(map[string]interface{})
	if got["clusterIssuer"] != "zerossl" {
		t.Errorf("clusterIssuer = %v, want zerossl", got["clusterIssuer"])
	}
	if got["domain"] != "new.example.com" {
		t.Errorf("domain = %v, want the patched value", got["domain"])
	}
}

func TestRestoreIngressCertFieldsPreservesManualCertificate(t *testing.T) {
	stored := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"tlsSecretName": "wildcard-example-com",
			"tlsMode":       "manual",
		}),
	}
	existing := collectIngressCertFields(stored)

	spec := map[string]interface{}{"ingress": ingress(map[string]interface{}{"tls": true})}
	restoreIngressCertFields(spec, existing)

	got := spec["ingress"].(map[string]interface{})
	if got["tlsSecretName"] != "wildcard-example-com" {
		t.Errorf("tlsSecretName = %v, want it preserved", got["tlsSecretName"])
	}
	if got["tlsMode"] != "manual" {
		t.Errorf("tlsMode = %v, want it preserved", got["tlsMode"])
	}
}

// Preserving an omitted field must not make the field unclearable — an explicit null is
// how the UI switches an app back to the instance default.
func TestRestoreIngressCertFieldsHonoursExplicitClear(t *testing.T) {
	stored := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{"clusterIssuer": "zerossl"}),
	}
	existing := collectIngressCertFields(stored)

	spec := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{"tls": true, "clusterIssuer": nil}),
	}
	restoreIngressCertFields(spec, existing)

	got := spec["ingress"].(map[string]interface{})
	if _, present := got["clusterIssuer"]; present {
		t.Errorf("clusterIssuer = %v, want it cleared by the explicit null", got["clusterIssuer"])
	}
}

func TestRestoreIngressCertFieldsPerEnvironment(t *testing.T) {
	stored := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{
				"name":    "staging",
				"ingress": ingress(map[string]interface{}{"clusterIssuer": "letsencrypt-staging"}),
			},
			map[string]interface{}{
				"name":    "production",
				"ingress": ingress(map[string]interface{}{"clusterIssuer": "letsencrypt-prod"}),
			},
		},
	}
	existing := collectIngressCertFields(stored)

	// A patch that rewrites both environments' domains and omits both issuers.
	spec := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{
				"name":    "staging",
				"ingress": ingress(map[string]interface{}{"domains": []interface{}{"stg.example.com"}}),
			},
			map[string]interface{}{
				"name":    "production",
				"ingress": ingress(map[string]interface{}{"domains": []interface{}{"example.com"}}),
			},
		},
	}
	restoreIngressCertFields(spec, existing)

	envs := spec["environments"].([]interface{})
	for i, want := range []string{"letsencrypt-staging", "letsencrypt-prod"} {
		env := envs[i].(map[string]interface{})
		got := env["ingress"].(map[string]interface{})["clusterIssuer"]
		if got != want {
			t.Errorf("environment %d clusterIssuer = %v, want %s", i, got, want)
		}
	}
}

// An environment that never had an issuer must not inherit another environment's.
func TestRestoreIngressCertFieldsDoesNotLeakBetweenEnvironments(t *testing.T) {
	stored := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{
				"name":    "production",
				"ingress": ingress(map[string]interface{}{"clusterIssuer": "letsencrypt-prod"}),
			},
		},
	}
	existing := collectIngressCertFields(stored)

	spec := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{"name": "preview", "ingress": ingress(map[string]interface{}{"tls": true})},
		},
	}
	restoreIngressCertFields(spec, existing)

	got := spec["environments"].([]interface{})[0].(map[string]interface{})["ingress"].(map[string]interface{})
	if v, present := got["clusterIssuer"]; present {
		t.Errorf("preview inherited clusterIssuer = %v, want none", v)
	}
}

func TestValidateIngressCertAnnotationsRejectsNewAnnotation(t *testing.T) {
	patch := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"annotations": map[string]interface{}{ClusterIssuerAnnotation: "zerossl"},
		}),
	}

	err := validateIngressCertAnnotations(patch, map[string]interface{}{})
	if err == nil {
		t.Fatal("expected an error for a newly added issuer annotation")
	}
}

func TestValidateIngressCertAnnotationsRejectsChangedAnnotation(t *testing.T) {
	existing := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"annotations": map[string]interface{}{ClusterIssuerAnnotation: "old-ca"},
		}),
	}
	patch := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"annotations": map[string]interface{}{ClusterIssuerAnnotation: "new-ca"},
		}),
	}

	if err := validateIngressCertAnnotations(patch, existing); err == nil {
		t.Fatal("expected an error for a changed issuer annotation")
	}
}

// Installs predating the provider selector carry this annotation. Saving an unrelated
// change must not start failing for them — they are migrated when the app is next edited
// in the UI, not by being locked out of every save until then.
func TestValidateIngressCertAnnotationsGrandfathersUnchanged(t *testing.T) {
	stored := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"annotations": map[string]interface{}{ClusterIssuerAnnotation: "internal-ca"},
		}),
	}
	patch := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"domain":      "new.example.com",
			"annotations": map[string]interface{}{ClusterIssuerAnnotation: "internal-ca"},
		}),
	}

	if err := validateIngressCertAnnotations(patch, stored); err != nil {
		t.Fatalf("unchanged legacy annotation was rejected: %v", err)
	}
}

func TestValidateIngressCertAnnotationsChecksEnvironments(t *testing.T) {
	patch := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{
				"name": "staging",
				"ingress": ingress(map[string]interface{}{
					"annotations": map[string]interface{}{ClusterIssuerAnnotation: "staging-ca"},
				}),
			},
		},
	}

	if err := validateIngressCertAnnotations(patch, map[string]interface{}{}); err == nil {
		t.Fatal("expected an error for a per-environment issuer annotation")
	}
}

// Other annotations are the whole point of the free-form map and must pass through.
func TestValidateIngressCertAnnotationsAllowsOtherKeys(t *testing.T) {
	patch := map[string]interface{}{
		"ingress": ingress(map[string]interface{}{
			"annotations": map[string]interface{}{
				"nginx.ingress.kubernetes.io/limit-rps":    "10",
				"traefik.ingress.kubernetes.io/router.tls": "true",
			},
		}),
	}

	if err := validateIngressCertAnnotations(patch, map[string]interface{}{}); err != nil {
		t.Fatalf("unrelated annotations were rejected: %v", err)
	}
}
