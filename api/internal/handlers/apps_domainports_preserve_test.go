package handlers

import "testing"

// A save that does not mention domainPorts must not send a domain back to the default
// port. The dashboard's environment branch rebuilds each env ingress from scratch, so it
// relies on this as much as the CLI does.
func TestDomainPortsSurviveAPatchThatOmitsThem(t *testing.T) {
	stored := map[string]interface{}{
		"ingress": map[string]interface{}{
			"domain":      "app.example.com",
			"domainPorts": map[string]interface{}{"admin.example.com": "admin"},
		},
		"environments": []interface{}{
			map[string]interface{}{"name": "prod", "ingress": map[string]interface{}{
				"domains":     []interface{}{"app.example.com", "admin.example.com"},
				"domainPorts": map[string]interface{}{"admin.example.com": "admin"},
			}},
		},
	}
	existing := collectPreservedIngressFields(stored)

	spec := map[string]interface{}{
		"ingress": map[string]interface{}{"domain": "app.example.com"},
		"environments": []interface{}{
			map[string]interface{}{"name": "prod", "ingress": map[string]interface{}{
				"domains": []interface{}{"app.example.com", "admin.example.com"},
			}},
		},
	}
	restorePreservedIngressFields(spec, existing)

	if got := spec["ingress"].(map[string]interface{})["domainPorts"]; got == nil {
		t.Error("app-level domainPorts dropped")
	}
	env := spec["environments"].([]interface{})[0].(map[string]interface{})["ingress"].(map[string]interface{})
	dp, _ := env["domainPorts"].(map[string]interface{})
	if dp["admin.example.com"] != "admin" {
		t.Errorf("env domainPorts = %v, want admin.example.com: admin", env["domainPorts"])
	}
}

// An explicit null is how the dashboard says "back to the default port for every domain".
func TestDomainPortsExplicitNullClears(t *testing.T) {
	stored := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{"name": "prod", "ingress": map[string]interface{}{
				"domainPorts": map[string]interface{}{"admin.example.com": "admin"},
			}},
		},
	}
	existing := collectPreservedIngressFields(stored)
	spec := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{"name": "prod", "ingress": map[string]interface{}{"domainPorts": nil}},
		},
	}
	restorePreservedIngressFields(spec, existing)
	env := spec["environments"].([]interface{})[0].(map[string]interface{})["ingress"].(map[string]interface{})
	if _, ok := env["domainPorts"]; ok {
		t.Errorf("explicit null should clear domainPorts, got %v", env["domainPorts"])
	}
}
