package handlers

import (
	"fmt"
	"strings"
	"testing"
)

// Nothing from the request may reach the stored resource unless logDrainSpecFrom names it.
// The spec is built key by key for that reason: a blocklist of field names only stops the
// ones I thought of, where a whitelist stops everything.
func TestNoCredentialReachesTheStoredDrainSpec(t *testing.T) {
	req := logDrainRequest{
		Name: "central",
		Type: "datadog",
		Config: map[string]interface{}{
			"site":    "datadoghq.eu",
			"service": "api",
		},
		Credentials: map[string]string{"apiKey": "dd-secret-value"},
	}

	spec := logDrainSpecFrom(req)
	encoded := fmt.Sprintf("%v", spec)

	if strings.Contains(encoded, "dd-secret-value") {
		t.Errorf("the API key reached the stored spec: %v", spec)
	}
	if strings.Contains(encoded, "credentials") {
		t.Errorf("the credentials map reached the stored spec: %v", spec)
	}
	// The non-secret configuration must survive.
	body, ok := spec["datadog"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a datadog body, got %v", spec)
	}
	if body["site"] != "datadoghq.eu" {
		t.Errorf("site was lost: %v", body)
	}
}

func TestLogDrainSpecOmitsEmptyScope(t *testing.T) {
	spec := logDrainSpecFrom(logDrainRequest{Name: "x", Type: "http",
		Config: map[string]interface{}{"uri": "https://example.com"}})
	for _, field := range []string{"project", "app", "environment", "displayName", "description"} {
		if _, present := spec[field]; present {
			t.Errorf("empty %q should not be written to the spec", field)
		}
	}
	// enabled is a pointer in the CRD precisely so that absent and false differ; an absent
	// one must not be stored as false.
	if _, present := spec["enabled"]; present {
		t.Error("an unset enabled must not be stored")
	}
}

func TestLogDrainSpecStoresExplicitDisable(t *testing.T) {
	disabled := false
	spec := logDrainSpecFrom(logDrainRequest{Name: "x", Type: "http", Enabled: &disabled,
		Config: map[string]interface{}{"uri": "https://example.com"}})
	if spec["enabled"] != false {
		t.Errorf("an explicit disable is how an app opts out; it must be stored: %v", spec)
	}
}

func TestValidateLogDrain(t *testing.T) {
	valid := func(mutate func(*logDrainRequest)) logDrainRequest {
		req := logDrainRequest{Name: "d", Type: "http",
			Config: map[string]interface{}{"uri": "https://logs.example.com"}}
		if mutate != nil {
			mutate(&req)
		}
		return req
	}

	t.Run("accepts a valid http drain", func(t *testing.T) {
		if err := validateLogDrain(valid(nil)); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects an unknown type and names the valid ones", func(t *testing.T) {
		err := validateLogDrain(valid(func(r *logDrainRequest) { r.Type = "carrier-pigeon" }))
		if err == nil || !strings.Contains(err.Error(), "datadog") {
			t.Errorf("expected an error listing valid types, got: %v", err)
		}
	})

	t.Run("rejects a uri that is not http", func(t *testing.T) {
		// A bare hostname renders an output the collector accepts and that silently never
		// delivers.
		err := validateLogDrain(valid(func(r *logDrainRequest) {
			r.Config = map[string]interface{}{"uri": "logs.example.com"}
		}))
		if err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("rejects an environment with no project", func(t *testing.T) {
		// Namespaces are "<project>-<env>", so an environment alone resolves to nothing
		// and the drain would silently match no logs.
		err := validateLogDrain(valid(func(r *logDrainRequest) { r.Environment = "production" }))
		if err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("accepts an environment with a project", func(t *testing.T) {
		err := validateLogDrain(valid(func(r *logDrainRequest) {
			r.Project, r.Environment = "shop", "production"
		}))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("datadog needs an api key", func(t *testing.T) {
		err := validateLogDrain(logDrainRequest{Name: "d", Type: "datadog",
			Config: map[string]interface{}{"site": "datadoghq.eu"}})
		if err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("datadog accepts an existing reference on edit", func(t *testing.T) {
		// Editing without resubmitting the key must not fail: it cannot be read back.
		err := validateLogDrain(logDrainRequest{Name: "d", Type: "datadog",
			Config: map[string]interface{}{
				"site":   "datadoghq.eu",
				"apiKey": map[string]interface{}{"name": "vld-d-credentials", "key": "apiKey"},
			}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("host-based drains need a host", func(t *testing.T) {
		for _, drainType := range []string{"loki", "syslog", "elasticsearch"} {
			err := validateLogDrain(logDrainRequest{Name: "d", Type: drainType,
				Config: map[string]interface{}{"port": 1234}})
			if err == nil {
				t.Errorf("%s should require a host", drainType)
			}
		}
	})

	t.Run("s3 needs a bucket", func(t *testing.T) {
		err := validateLogDrain(logDrainRequest{Name: "d", Type: "s3",
			Config: map[string]interface{}{"region": "eu-west-1"}})
		if err == nil {
			t.Error("expected an error")
		}
	})
}

// Every drain type must have an entry, even an empty one. A missing entry means the
// handler never strips that type's credential fields, so a value submitted under a
// credential key would be stored on the resource.
func TestEveryDrainTypeHasCredentialFields(t *testing.T) {
	for drainType := range drainTypes {
		if _, ok := credentialFields[drainType]; !ok {
			t.Errorf("type %q has no credentialFields entry; its credentials would not be stripped", drainType)
		}
	}
}

func TestDrainSecretName(t *testing.T) {
	if got := drainSecretName("central"); got != "vld-central-credentials" {
		t.Errorf("got %q", got)
	}
}

func TestLogDrainAcceptsYAML(t *testing.T) {
	// Reuses the middleware parser, so a drain can be pasted the same way a middleware can.
	req := logDrainRequest{Type: "loki", ConfigRaw: "host: loki.monitoring.svc\nport: 3100"}
	if err := req.resolveRaw(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Config["host"] != "loki.monitoring.svc" {
		t.Errorf("got %#v", req.Config)
	}
}
