package handlers

import (
	"reflect"
	"testing"
)

// Attaching a middleware and then editing anything else about the app detached it.
//
// The attachment lives at spec.environments[].ingress.middlewares, written by a different
// endpoint than the one the edit form calls. The form rebuilds each environment's ingress
// from the fields it renders, and it does not render middlewares -- so every save dropped
// the key, and the app quietly lost its auth, rate limit or allow-list.
//
// Nothing failed. The save succeeded, the form showed what the user had typed, and the
// middleware was simply gone from the routes.
func TestEditingAnAppKeepsItsMiddlewares(t *testing.T) {
	spec := map[string]interface{}{
		"ingress": map[string]interface{}{
			"domain":      "app.example.com",
			"middlewares": []interface{}{"rate-limit", "basic-auth"},
		},
		"environments": []interface{}{
			map[string]interface{}{
				"name": "production",
				"ingress": map[string]interface{}{
					"domains":     []interface{}{"prod.example.com"},
					"middlewares": []interface{}{"allow-list", "basic-auth"},
				},
			},
		},
	}

	previous := collectPreservedIngressFields(spec)

	// What the edit form sends: ingress rebuilt from the fields it renders, no middlewares.
	patched := map[string]interface{}{
		"ingress": map[string]interface{}{
			"domain": "app.example.com",
			"tls":    true,
		},
		"environments": []interface{}{
			map[string]interface{}{
				"name": "production",
				"ingress": map[string]interface{}{
					"domains": []interface{}{"prod.example.com"},
					"tls":     true,
				},
			},
		},
	}

	restorePreservedIngressFields(patched, previous)

	appIng, _, _ := unstructuredNestedMap(patched, "ingress")
	if got := appIng["middlewares"]; !reflect.DeepEqual(got, []interface{}{"rate-limit", "basic-auth"}) {
		t.Errorf("app-level middlewares = %v, want them preserved", got)
	}

	envs, _, _ := unstructuredNestedSlice(patched, "environments")
	envIng, _ := envs[0].(map[string]interface{})["ingress"].(map[string]interface{})
	if got := envIng["middlewares"]; !reflect.DeepEqual(got, []interface{}{"allow-list", "basic-auth"}) {
		t.Errorf("environment middlewares = %v, want them preserved", got)
	}
}

// Order is semantic in Traefik -- an allowList before an auth check rejects strangers
// without prompting for a password, the reverse prompts first -- so preserving the list
// must preserve its order, not just its membership.
func TestPreservedMiddlewareOrderIsExact(t *testing.T) {
	spec := map[string]interface{}{
		"ingress": map[string]interface{}{
			"middlewares": []interface{}{"allow-list", "basic-auth", "rate-limit"},
		},
	}
	previous := collectPreservedIngressFields(spec)

	patched := map[string]interface{}{"ingress": map[string]interface{}{"domain": "x"}}
	restorePreservedIngressFields(patched, previous)

	ing, _, _ := unstructuredNestedMap(patched, "ingress")
	want := []interface{}{"allow-list", "basic-auth", "rate-limit"}
	if !reflect.DeepEqual(ing["middlewares"], want) {
		t.Errorf("= %v, want %v in that order", ing["middlewares"], want)
	}
}

// Detaching has to remain possible. A patch that names the field wins, so an explicit empty
// list pins the app to no middlewares and an explicit null clears the key entirely --
// which is how an environment goes back to inheriting the app-level list.
func TestAnExplicitChangeStillWins(t *testing.T) {
	previous := map[string]map[string]interface{}{
		"": {"middlewares": []interface{}{"basic-auth"}},
	}

	t.Run("empty list detaches", func(t *testing.T) {
		patched := map[string]interface{}{
			"ingress": map[string]interface{}{"middlewares": []interface{}{}},
		}
		restorePreservedIngressFields(patched, previous)
		ing, _, _ := unstructuredNestedMap(patched, "ingress")
		if got, _ := ing["middlewares"].([]interface{}); len(got) != 0 {
			t.Errorf("= %v, want the explicit empty list to survive", got)
		}
	})

	t.Run("explicit null restores inheritance", func(t *testing.T) {
		patched := map[string]interface{}{
			"ingress": map[string]interface{}{"middlewares": nil},
		}
		restorePreservedIngressFields(patched, previous)
		ing, _, _ := unstructuredNestedMap(patched, "ingress")
		if _, present := ing["middlewares"]; present {
			t.Error("an explicit null left the key in place; the environment cannot go back " +
				"to inheriting the app-level list")
		}
	})
}

// An app that never had middlewares must not gain the key, or every app would be pinned to
// an empty list and stop inheriting.
func TestNoMiddlewaresStaysAbsent(t *testing.T) {
	previous := collectPreservedIngressFields(map[string]interface{}{
		"ingress": map[string]interface{}{"domain": "x"},
	})
	patched := map[string]interface{}{"ingress": map[string]interface{}{"domain": "x", "tls": true}}
	restorePreservedIngressFields(patched, previous)

	ing, _, _ := unstructuredNestedMap(patched, "ingress")
	if _, present := ing["middlewares"]; present {
		t.Error("an app with no middlewares gained the key")
	}
}
