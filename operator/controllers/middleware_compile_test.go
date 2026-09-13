package controllers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// The order of this annotation is its entire meaning -- Traefik runs middlewares in the
// order listed, so an allowList after an auth check prompts strangers for a password
// before rejecting them. These cases pin the order down rather than just the membership.
func TestComposeMiddlewareAnnotation(t *testing.T) {
	cases := []struct {
		name     string
		platform []string
		app      []string
		existing []string
		want     string
	}{
		{
			name: "empty everywhere yields an empty annotation, not a stray comma",
			want: "",
		},
		{
			name:     "platform redirect comes before app middlewares",
			platform: []string{"ns-app-https-redirect@kubernetescrd"},
			app:      []string{"ns-vmw-allow@kubernetescrd", "ns-vmw-auth@kubernetescrd"},
			want:     "ns-app-https-redirect@kubernetescrd,ns-vmw-allow@kubernetescrd,ns-vmw-auth@kubernetescrd",
		},
		{
			name: "app order is preserved exactly as declared",
			app:  []string{"ns-vmw-c@kubernetescrd", "ns-vmw-a@kubernetescrd", "ns-vmw-b@kubernetescrd"},
			want: "ns-vmw-c@kubernetescrd,ns-vmw-a@kubernetescrd,ns-vmw-b@kubernetescrd",
		},
		{
			name:     "a user-typed duplicate of the platform ref is not repeated",
			platform: []string{"ns-app-https-redirect@kubernetescrd"},
			existing: []string{"ns-app-https-redirect@kubernetescrd"},
			want:     "ns-app-https-redirect@kubernetescrd",
		},
		{
			name:     "user annotations survive rather than being overwritten",
			platform: []string{"ns-app-https-redirect@kubernetescrd"},
			app:      []string{"ns-vmw-allow@kubernetescrd"},
			existing: []string{"other-ns-custom@kubernetescrd"},
			want:     "ns-app-https-redirect@kubernetescrd,ns-vmw-allow@kubernetescrd,other-ns-custom@kubernetescrd",
		},
		{
			name: "whitespace and empty entries are dropped",
			app:  []string{" ns-vmw-a@kubernetescrd ", "", "   "},
			want: "ns-vmw-a@kubernetescrd",
		},
		{
			name: "a middleware listed twice by the app runs once",
			app:  []string{"ns-vmw-a@kubernetescrd", "ns-vmw-a@kubernetescrd"},
			want: "ns-vmw-a@kubernetescrd",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := composeMiddlewareAnnotation(tc.platform, tc.app, tc.existing)
			if got != tc.want {
				t.Errorf("composeMiddlewareAnnotation()\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// A nil per-environment list inherits; a non-nil one replaces, including when empty.
// The empty case is the reason the field is a pointer, so it is the one that matters.
func TestResolveAppMiddlewares(t *testing.T) {
	appWith := func(names ...string) *vestav1alpha1.VestaApp {
		return &vestav1alpha1.VestaApp{
			Spec: vestav1alpha1.VestaAppSpec{
				Ingress: &vestav1alpha1.IngressConfig{Middlewares: names},
			},
		}
	}
	envWith := func(list *[]string) vestav1alpha1.AppEnvironmentConfig {
		return vestav1alpha1.AppEnvironmentConfig{
			Name:    "production",
			Ingress: &vestav1alpha1.IngressOverride{Middlewares: list},
		}
	}
	empty := []string{}
	override := []string{"env-only"}

	t.Run("nil environment list inherits the app list", func(t *testing.T) {
		got := resolveAppMiddlewares(appWith("a", "b"), envWith(nil))
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Errorf("got %v, want [a b]", got)
		}
	})

	t.Run("empty environment list overrides the app list with nothing", func(t *testing.T) {
		got := resolveAppMiddlewares(appWith("a", "b"), envWith(&empty))
		if len(got) != 0 {
			t.Errorf("got %v, want an empty list -- an env must be able to opt out", got)
		}
	})

	t.Run("non-empty environment list replaces the app list entirely", func(t *testing.T) {
		got := resolveAppMiddlewares(appWith("a", "b"), envWith(&override))
		if !reflect.DeepEqual(got, []string{"env-only"}) {
			t.Errorf("got %v, want [env-only]", got)
		}
	})

	t.Run("no ingress config at all is not a panic", func(t *testing.T) {
		got := resolveAppMiddlewares(&vestav1alpha1.VestaApp{}, vestav1alpha1.AppEnvironmentConfig{Name: "x"})
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}

func TestCompileMiddleware(t *testing.T) {
	t.Run("rateLimit emits its configured fields", func(t *testing.T) {
		got, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type: "rateLimit",
			RateLimit: &vestav1alpha1.RateLimitMiddleware{
				Average: 100, Burst: 50, Period: "1m",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		body, ok := got["rateLimit"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected a rateLimit body, got %#v", got)
		}
		if body["average"] != float64(100) || body["burst"] != float64(50) || body["period"] != "1m" {
			t.Errorf("unexpected body: %#v", body)
		}
	})

	t.Run("omitempty keeps unset fields out of the emitted spec", func(t *testing.T) {
		// Traefik distinguishes an absent field from an explicit zero for several of
		// these, so an unset Burst must not appear as burst: 0.
		got, _ := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type:      "rateLimit",
			RateLimit: &vestav1alpha1.RateLimitMiddleware{Average: 10},
		})
		body := got["rateLimit"].(map[string]interface{})
		if _, present := body["burst"]; present {
			t.Errorf("unset burst leaked into the spec: %#v", body)
		}
	})

	t.Run("compress with no body is a complete configuration", func(t *testing.T) {
		got, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{Type: "compress"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["compress"]; !ok {
			t.Errorf("expected a compress key, got %#v", got)
		}
	})

	t.Run("basicAuth refuses to compile without a secret", func(t *testing.T) {
		_, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type:      "basicAuth",
			BasicAuth: &vestav1alpha1.BasicAuthMiddleware{Realm: "staging"},
		})
		if err == nil {
			t.Fatal("expected an error: credentials must come from a Secret")
		}
		if !strings.Contains(err.Error(), "secretName") {
			t.Errorf("error should name the missing field, got: %v", err)
		}
	})

	t.Run("basicAuth never emits credentials inline", func(t *testing.T) {
		got, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type:      "basicAuth",
			BasicAuth: &vestav1alpha1.BasicAuthMiddleware{SecretName: "htpasswd", Realm: "staging"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		encoded, _ := json.Marshal(got)
		for _, forbidden := range []string{"users", "password", "htpasswd:"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Errorf("compiled basicAuth leaked %q: %s", forbidden, encoded)
			}
		}
		body := got["basicAuth"].(map[string]interface{})
		if body["secret"] != "htpasswd" {
			t.Errorf("expected a secret reference, got %#v", body)
		}
	})

	t.Run("an empty ipAllowList is rejected rather than locking everyone out", func(t *testing.T) {
		_, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type:        "ipAllowList",
			IPAllowList: &vestav1alpha1.IPAllowListMiddleware{},
		})
		if err == nil {
			t.Fatal("expected an error: an empty allowList rejects all traffic")
		}
	})

	t.Run("a type whose body is missing is rejected", func(t *testing.T) {
		_, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{Type: "headers"})
		if err == nil {
			t.Fatal("expected an error for a headers middleware with no headers block")
		}
	})

	t.Run("a body that does not match its type is rejected", func(t *testing.T) {
		// The dangerous case: this would otherwise compile to an empty middleware that
		// Traefik loads happily and that silently does nothing.
		_, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type:      "headers",
			RateLimit: &vestav1alpha1.RateLimitMiddleware{Average: 10},
		})
		if err == nil {
			t.Fatal("expected an error when the body does not match the declared type")
		}
	})

	t.Run("raw passes through verbatim", func(t *testing.T) {
		got, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type: "raw",
			Raw:  &apiextensionsv1.JSON{Raw: []byte(`{"plugin":{"demo":{"headerName":"X-Demo"}}}`)},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		plugin, ok := got["plugin"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected the plugin key to survive, got %#v", got)
		}
		demo := plugin["demo"].(map[string]interface{})
		if demo["headerName"] != "X-Demo" {
			t.Errorf("raw body was altered: %#v", got)
		}
	})

	t.Run("raw with two middleware types is rejected", func(t *testing.T) {
		// Traefik's Middleware spec is a one-of. Two keys is undefined behaviour, and
		// rejecting it here is the only place the author still sees why.
		_, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
			Type: "raw",
			Raw:  &apiextensionsv1.JSON{Raw: []byte(`{"compress":{},"retry":{"attempts":2}}`)},
		})
		if err == nil {
			t.Fatal("expected an error for a raw body with two middleware types")
		}
	})

	t.Run("an unknown type is rejected", func(t *testing.T) {
		if _, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{Type: "teleport"}); err == nil {
			t.Fatal("expected an error for an unknown middleware type")
		}
	})
}

// Projected names must not collide with the two Middleware objects the app controller has
// always created for itself in the same namespaces.
func TestProjectedNamesCannotCollideWithBuiltIns(t *testing.T) {
	builtIns := []string{"my-app-https-redirect", "my-app-production"}
	for _, builtIn := range builtIns {
		// The collision would need a VestaMiddleware whose name produced the built-in
		// name once prefixed, which the prefix makes impossible.
		if projectedMiddlewareName(builtIn) == builtIn {
			t.Errorf("projection of %q collides with the built-in object", builtIn)
		}
		if !strings.HasPrefix(projectedMiddlewareName(builtIn), middlewareProjectionPrefix) {
			t.Errorf("projection of %q lost its prefix", builtIn)
		}
	}
}

func TestTraefikMiddlewareRef(t *testing.T) {
	if got := traefikMiddlewareRef("shop-production", "vmw-allow"); got != "shop-production-vmw-allow@kubernetescrd" {
		t.Errorf("got %q", got)
	}
}
