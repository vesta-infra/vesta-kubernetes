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
		Credentials: map[string]string{"API_KEY": "dd-secret-value"},
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

// Every drain type must have a credentialInputs entry, even an empty one, and every entry
// must name a config field. A missing one means the type's credential keys are never
// stripped from a submitted config, so a literal could be stored on the resource.
func TestEveryDrainTypeHasCredentialInputs(t *testing.T) {
	for drainType := range drainTypes {
		inputs, ok := credentialInputs[drainType]
		if !ok {
			t.Errorf("type %q has no credentialInputs entry", drainType)
			continue
		}
		primaries := map[string]int{}
		for _, input := range inputs {
			if input.SecretKey == "" || input.ConfigField == "" {
				t.Errorf("%s has an incomplete credential input: %+v", drainType, input)
			}
			if input.Primary {
				primaries[input.ConfigField]++
			}
		}
		// Exactly one primary per config field, or the reference is either never set or
		// set twice from different keys.
		for field, count := range primaries {
			if count != 1 {
				t.Errorf("%s.%s has %d primary keys, want 1", drainType, field, count)
			}
		}
		for _, field := range credentialConfigFields(drainType) {
			if primaries[field] != 1 {
				t.Errorf("%s.%s has no primary key, so its reference is never set", drainType, field)
			}
		}
	}
}

func TestS3AndOpenObserveShareAFieldButNotKeys(t *testing.T) {
	// Both use the config field "credentials", but the collector settings differ --
	// aws_access_key_id versus http_user -- so the Secret keys must differ too.
	keyFor := func(drainType string) string {
		for _, input := range credentialInputs[drainType] {
			if input.Primary {
				return input.SecretKey
			}
		}
		return ""
	}
	if keyFor("s3") != "AWS_ACCESS_KEY_ID" {
		t.Errorf("s3 primary key is %q", keyFor("s3"))
	}
	if keyFor("openobserve") != "USER" {
		t.Errorf("openobserve primary key is %q", keyFor("openobserve"))
	}
}

func TestValidateOpenObserve(t *testing.T) {
	base := func(endpoint string) logDrainRequest {
		return logDrainRequest{Name: "oo", Type: "openobserve",
			Config: map[string]interface{}{"endpoint": endpoint}}
	}

	t.Run("accepts a base URL", func(t *testing.T) {
		if err := validateLogDrain(base("https://openobserve.example.com")); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects an endpoint carrying a path", func(t *testing.T) {
		// The ingest path is built from organization and stream; a path here would be
		// prefixed to it and 404.
		err := validateLogDrain(base("https://openobserve.example.com/api/default/vesta/_json"))
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "without a path") {
			t.Errorf("the error should say why, got: %v", err)
		}
	})

	t.Run("rejects a bare hostname", func(t *testing.T) {
		if err := validateLogDrain(base("openobserve.example.com")); err == nil {
			t.Error("expected an error")
		}
	})
}
