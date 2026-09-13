package handlers

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The API and the operator both decide which middlewares apply to an environment. If they
// disagree, the UI shows one thing and Traefik serves another -- so these cases mirror
// TestResolveAppMiddlewares in the operator exactly.
func TestResolveMiddlewaresForEnv(t *testing.T) {
	appWith := func(appLevel []interface{}, envIngress map[string]interface{}) map[string]interface{} {
		env := map[string]interface{}{"name": "production"}
		if envIngress != nil {
			env["ingress"] = envIngress
		}
		spec := map[string]interface{}{
			"environments": []interface{}{env},
		}
		if appLevel != nil {
			spec["ingress"] = map[string]interface{}{"middlewares": appLevel}
		}
		return map[string]interface{}{"spec": spec}
	}

	t.Run("absent environment list inherits the app list", func(t *testing.T) {
		names, inherited := resolveMiddlewaresForEnv(appWith([]interface{}{"a", "b"}, nil), "production")
		if !reflect.DeepEqual(names, []string{"a", "b"}) {
			t.Errorf("got %v, want [a b]", names)
		}
		if !inherited {
			t.Error("expected inherited=true so the UI can say so")
		}
	})

	t.Run("empty environment list applies no middlewares", func(t *testing.T) {
		// The case the pointer field exists for: an env opting out of a platform-wide
		// middleware without the app having to stop declaring it.
		names, inherited := resolveMiddlewaresForEnv(
			appWith([]interface{}{"a"}, map[string]interface{}{"middlewares": []interface{}{}}), "production")
		if len(names) != 0 {
			t.Errorf("got %v, want none", names)
		}
		if inherited {
			t.Error("an explicit empty list is not inheritance")
		}
	})

	t.Run("environment list replaces the app list", func(t *testing.T) {
		names, inherited := resolveMiddlewaresForEnv(
			appWith([]interface{}{"a"}, map[string]interface{}{"middlewares": []interface{}{"b", "c"}}), "production")
		if !reflect.DeepEqual(names, []string{"b", "c"}) {
			t.Errorf("got %v, want [b c]", names)
		}
		if inherited {
			t.Error("expected inherited=false")
		}
	})

	t.Run("order is preserved", func(t *testing.T) {
		names, _ := resolveMiddlewaresForEnv(
			appWith(nil, map[string]interface{}{"middlewares": []interface{}{"z", "a", "m"}}), "production")
		if !reflect.DeepEqual(names, []string{"z", "a", "m"}) {
			t.Errorf("got %v, want [z a m] -- order is the meaning of this list", names)
		}
	})

	t.Run("an app with no ingress config at all yields nothing", func(t *testing.T) {
		names, _ := resolveMiddlewaresForEnv(map[string]interface{}{"spec": map[string]interface{}{}}, "production")
		if len(names) != 0 {
			t.Errorf("got %v, want none", names)
		}
	})
}

func TestValidateMiddlewarePayload(t *testing.T) {
	t.Run("rejects an unknown type and names the valid ones", func(t *testing.T) {
		err := validateMiddlewarePayload(middlewareRequest{Type: "teleport", Config: map[string]interface{}{"x": 1}})
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "rateLimit") {
			t.Errorf("error should list valid types, got: %v", err)
		}
	})

	t.Run("compress alone is valid", func(t *testing.T) {
		if err := validateMiddlewarePayload(middlewareRequest{Type: "compress"}); err != nil {
			t.Errorf("compress needs no configuration, got: %v", err)
		}
	})

	t.Run("other types need a configuration", func(t *testing.T) {
		if err := validateMiddlewarePayload(middlewareRequest{Type: "rateLimit"}); err == nil {
			t.Error("expected an error for a rateLimit with no configuration")
		}
	})

	t.Run("basicAuth needs users or a secret name", func(t *testing.T) {
		err := validateMiddlewarePayload(middlewareRequest{
			Type: "basicAuth", Config: map[string]interface{}{"realm": "staging"},
		})
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	// Credentials may travel in the request -- that is how the UI works -- but nothing
	// resembling one may reach the resource. The config that gets stored is built key by
	// key rather than copied, so this holds whatever the caller sends.
	t.Run("no submitted field can smuggle a password into the stored config", func(t *testing.T) {
		stored := basicAuthConfigFor("gate", []string{"alice"}, map[string]interface{}{
			"realm":        "staging",
			"password":     "hunter2",
			"passwords":    []string{"hunter2"},
			"users":        []interface{}{map[string]interface{}{"username": "alice", "password": "hunter2"}},
			"htpasswd":     "alice:$apr1$xyz",
			"secretName":   "attacker-controlled",
			"anythingElse": "hunter2",
		})

		encoded := fmt.Sprintf("%v", stored)
		if strings.Contains(encoded, "hunter2") || strings.Contains(encoded, "$apr1$") {
			t.Errorf("a credential reached the stored config: %v", stored)
		}
		// secretName is derived, never taken from the caller -- otherwise a middleware
		// could be pointed at any Secret in vesta-system.
		if stored["secretName"] != "vmw-gate-auth" {
			t.Errorf("secretName should be derived from the name, got %v", stored["secretName"])
		}
		if stored["realm"] != "staging" {
			t.Errorf("realm should survive, got %v", stored["realm"])
		}
		users, _ := stored["users"].([]interface{})
		if len(users) != 1 || users[0] != "alice" {
			t.Errorf("only usernames should be stored, got %v", stored["users"])
		}
	})

	t.Run("raw must name exactly one middleware type", func(t *testing.T) {
		err := validateMiddlewarePayload(middlewareRequest{
			Type:   "raw",
			Config: map[string]interface{}{"compress": map[string]interface{}{}, "retry": map[string]interface{}{}},
		})
		if err == nil {
			t.Error("expected an error: the Traefik spec is a one-of")
		}
	})
}

func TestMiddlewareSpecFrom(t *testing.T) {
	t.Run("typed config lands under the field for its type", func(t *testing.T) {
		spec := middlewareSpecFrom(middlewareRequest{
			Type:   "rateLimit",
			Config: map[string]interface{}{"average": 100},
		})
		body, ok := spec["rateLimit"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected a rateLimit body, got %#v", spec)
		}
		if body["average"] != 100 {
			t.Errorf("unexpected body %#v", body)
		}
	})

	t.Run("raw config is stored verbatim under raw", func(t *testing.T) {
		spec := middlewareSpecFrom(middlewareRequest{
			Type:   "raw",
			Config: map[string]interface{}{"plugin": map[string]interface{}{"demo": true}},
		})
		raw, ok := spec["raw"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected a raw body, got %#v", spec)
		}
		if _, ok := raw["plugin"]; !ok {
			t.Errorf("raw body was altered: %#v", raw)
		}
	})

	t.Run("empty scope fields are omitted rather than stored blank", func(t *testing.T) {
		spec := middlewareSpecFrom(middlewareRequest{Type: "compress"})
		for _, field := range []string{"project", "app", "environment", "displayName", "description"} {
			if _, present := spec[field]; present {
				t.Errorf("empty %q should not be written to the spec", field)
			}
		}
	})
}

func TestFirstDuplicate(t *testing.T) {
	if got := firstDuplicate([]string{"a", "b", "a"}); got != "a" {
		t.Errorf("got %q, want a -- a middleware listed twice runs twice", got)
	}
	if got := firstDuplicate([]string{"a", "b"}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// bodyFieldForType must cover every accepted type, or a create silently drops its
// configuration: the spec would be written with only a type and compile to nothing.
func TestEveryMiddlewareTypeHasABodyField(t *testing.T) {
	for name := range middlewareTypes {
		if _, ok := bodyFieldForType[name]; !ok {
			t.Errorf("type %q has no body field mapping; its configuration would be dropped on save", name)
		}
	}
}
