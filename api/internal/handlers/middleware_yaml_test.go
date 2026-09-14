package handlers

import (
	"strings"
	"testing"
)

// The thing people will actually paste: a whole manifest, straight from Traefik's docs or
// from `kubectl get middleware -o yaml`. Requiring them to trim it down to the spec by
// hand is both annoying and a reliable way to lose a field.
func TestParseRawMiddlewareAcceptsAWholeManifest(t *testing.T) {
	got, err := parseRawMiddleware(`
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: coraza-forward-auth
  namespace: credpal-prod
spec:
  forwardAuth:
    address: http://coraza-traefik-middleware-waf.credpal-prod.svc.cluster.local:9080
    trustForwardHeader: true
    authRequestHeaders:
      - X-Forwarded-Method
      - X-Real-Ip
      - CF-Connecting-IP
      - Cookie
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fa, ok := got["forwardAuth"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected the spec body, got %#v", got)
	}
	if fa["trustForwardHeader"] != true {
		t.Errorf("trustForwardHeader lost: %#v", fa)
	}
	headers, _ := fa["authRequestHeaders"].([]interface{})
	if len(headers) != 4 {
		t.Errorf("got %d headers, want 4 -- a dropped header is one the WAF cannot inspect", len(headers))
	}
	// metadata must not survive into the spec; name and namespace are Vesta's to decide.
	for _, leaked := range []string{"metadata", "apiVersion", "kind"} {
		if _, present := got[leaked]; present {
			t.Errorf("%q leaked into the middleware spec", leaked)
		}
	}
}

func TestParseRawMiddleware(t *testing.T) {
	t.Run("a bare spec body works too", func(t *testing.T) {
		got, err := parseRawMiddleware("compress: {}")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["compress"]; !ok {
			t.Errorf("got %#v", got)
		}
	})

	t.Run("JSON still works", func(t *testing.T) {
		// JSON is valid YAML, so this needs no special case -- but it is the format the
		// field accepted before, and breaking it would break saved workflows.
		got, err := parseRawMiddleware(`{"retry": {"attempts": 3}}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		retry, ok := got["retry"].(map[string]interface{})
		if !ok || retry["attempts"] != float64(3) {
			t.Errorf("got %#v", got)
		}
	})

	t.Run("a leading document separator is tolerated", func(t *testing.T) {
		if _, err := parseRawMiddleware("---\ncompress: {}"); err != nil {
			t.Errorf("a copied YAML document often starts with ---: %v", err)
		}
	})

	t.Run("a multi-document paste is refused", func(t *testing.T) {
		// Taking the first silently would discard the rest without saying so.
		_, err := parseRawMiddleware("compress: {}\n---\nretry:\n  attempts: 2")
		if err == nil {
			t.Fatal("expected an error for a multi-document paste")
		}
	})

	t.Run("the wrong kind is named in the error", func(t *testing.T) {
		_, err := parseRawMiddleware(`
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
spec:
  routes: []
`)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "IngressRoute") {
			t.Errorf("the error should name what was pasted, got: %v", err)
		}
	})

	t.Run("a Middleware with no spec is refused", func(t *testing.T) {
		_, err := parseRawMiddleware("apiVersion: traefik.io/v1alpha1\nkind: Middleware\nmetadata:\n  name: x")
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("empty input is refused", func(t *testing.T) {
		for _, input := range []string{"", "   ", "\n\n"} {
			if _, err := parseRawMiddleware(input); err == nil {
				t.Errorf("expected an error for %q", input)
			}
		}
	})

	t.Run("malformed YAML reports a parse error", func(t *testing.T) {
		// Tabs are not legal YAML indentation, and an unclosed flow mapping cannot parse.
		for _, broken := range []string{
			"forwardAuth:\n\taddress: http://x",
			"forwardAuth: {address: http://x",
		} {
			if _, err := parseRawMiddleware(broken); err == nil {
				t.Errorf("expected a parse error for %q", broken)
			}
		}
	})

	t.Run("a scalar is not a middleware", func(t *testing.T) {
		// "compress" alone parses as a YAML string, not a mapping. Without a type check
		// that would reach Traefik as a spec it cannot interpret.
		if _, err := parseRawMiddleware("compress"); err == nil {
			t.Error("expected an error for a bare scalar")
		}
	})
}

func TestResolveRawConfig(t *testing.T) {
	t.Run("raw YAML lands in Config", func(t *testing.T) {
		req := middlewareRequest{Type: "raw", ConfigRaw: "compress: {}"}
		if err := req.resolveRawConfig(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := req.Config["compress"]; !ok {
			t.Errorf("got %#v", req.Config)
		}
	})

	t.Run("configRaw on a typed middleware is refused", func(t *testing.T) {
		// Accepting it would mean two sources of truth for the same body, with no rule
		// about which wins.
		req := middlewareRequest{Type: "rateLimit", ConfigRaw: "rateLimit:\n  average: 10"}
		if err := req.resolveRawConfig(); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("no configRaw leaves Config alone", func(t *testing.T) {
		req := middlewareRequest{Type: "raw", Config: map[string]interface{}{"compress": map[string]interface{}{}}}
		if err := req.resolveRawConfig(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := req.Config["compress"]; !ok {
			t.Errorf("existing config was clobbered: %#v", req.Config)
		}
	})
}
