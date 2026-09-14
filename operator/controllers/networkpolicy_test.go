package controllers

import (
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

// Isolation off must produce nothing, so turning it off removes the policies rather than
// leaving a deny in place that nothing will clean up.
func TestIsolationOffProducesNoPolicies(t *testing.T) {
	if got := BuildNetworkPolicies("acme-prod", NetworkIsolation{}); len(got) != 0 {
		t.Errorf("isolation is off but %d policies were rendered", len(got))
	}
}

// The failure this must never have: a default-deny that also denies egress. DNS stops
// resolving, and every symptom afterwards points somewhere else entirely.
func TestNothingDeniesEgress(t *testing.T) {
	policies := BuildNetworkPolicies("acme-prod", NetworkIsolation{Enabled: true, MetricsPort: 9090})
	if len(policies) == 0 {
		t.Fatal("isolation is on but nothing was rendered")
	}

	for _, p := range policies {
		for _, pt := range p.Spec.PolicyTypes {
			if pt == networkingv1.PolicyTypeEgress {
				t.Errorf("%s declares an Egress policy type; a default-deny egress breaks DNS "+
					"and the isolation asked for is entirely an ingress property", p.Name)
			}
		}
		if len(p.Spec.Egress) > 0 {
			t.Errorf("%s carries egress rules", p.Name)
		}
	}
}

// Turning isolation on must not break scale-to-zero: the activator proxies a request to the
// app it just woke, from inside the same namespace.
func TestSameNamespaceTrafficIsAllowed(t *testing.T) {
	policies := BuildNetworkPolicies("acme-prod", NetworkIsolation{Enabled: true})

	var allow *networkingv1.NetworkPolicy
	for _, p := range policies {
		if p.Name == policySameNamespace {
			allow = p
		}
	}
	if allow == nil {
		t.Fatal("no same-namespace allow rule; the activator could not reach the app it wakes, " +
			"and an app could not reach its own database")
	}

	if len(allow.Spec.Ingress) != 1 || len(allow.Spec.Ingress[0].From) != 1 {
		t.Fatalf("unexpected rule shape: %+v", allow.Spec.Ingress)
	}
	peer := allow.Spec.Ingress[0].From[0]
	if peer.PodSelector == nil {
		t.Error("the same-namespace rule does not select pods")
	}
	// A NamespaceSelector here would widen this to every namespace in the cluster, which is
	// the opposite of isolation -- an empty namespace selector matches all namespaces, not
	// the current one.
	if peer.NamespaceSelector != nil {
		t.Error("the same-namespace rule carries a namespace selector; an empty one matches " +
			"EVERY namespace and would allow the whole cluster in")
	}
}

// The deny has to actually deny: a policy with ingress rules is not a default deny.
func TestTheDenyIsADeny(t *testing.T) {
	policies := BuildNetworkPolicies("acme-prod", NetworkIsolation{Enabled: true})

	for _, p := range policies {
		if p.Name != policyDenyIngress {
			continue
		}
		if len(p.Spec.Ingress) != 0 {
			t.Error("the default-deny policy carries ingress rules, so it allows rather than denies")
		}
		if len(p.Spec.PodSelector.MatchLabels) != 0 || len(p.Spec.PodSelector.MatchExpressions) != 0 {
			t.Error("the default-deny policy selects only some pods; the rest stay unprotected")
		}
		if len(p.Spec.PolicyTypes) != 1 || p.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
			t.Errorf("policyTypes = %v, want [Ingress] exactly", p.Spec.PolicyTypes)
		}
		return
	}
	t.Fatal("no default-deny policy was rendered")
}

// Without a route in from the ingress controller, turning isolation on takes every app in the
// environment off the internet.
func TestIngressControllerCanStillReachApps(t *testing.T) {
	policies := BuildNetworkPolicies("acme-prod", NetworkIsolation{Enabled: true})

	var trusted *networkingv1.NetworkPolicy
	for _, p := range policies {
		if p.Name == policyFromTrusted {
			trusted = p
		}
	}
	if trusted == nil {
		t.Fatal("nothing allows the ingress controller in; every app would go dark")
	}

	values := trusted.Spec.Ingress[0].From[0].NamespaceSelector.MatchExpressions[0].Values
	got := map[string]bool{}
	for _, v := range values {
		got[v] = true
	}
	for _, want := range []string{"traefik", "ingress-nginx", "kube-system"} {
		if !got[want] {
			t.Errorf("the default trusted list omits %q", want)
		}
	}
}

func TestExplicitTrustReplacesTheDefaults(t *testing.T) {
	policies := BuildNetworkPolicies("acme-prod", NetworkIsolation{
		Enabled:           true,
		TrustedNamespaces: []string{"my-ingress"},
	})

	for _, p := range policies {
		if p.Name != policyFromTrusted {
			continue
		}
		values := p.Spec.Ingress[0].From[0].NamespaceSelector.MatchExpressions[0].Values
		if len(values) != 1 || values[0] != "my-ingress" {
			t.Errorf("trusted namespaces = %v, want exactly [my-ingress]; a configured list that "+
				"is merged with the defaults silently trusts more than was asked for", values)
		}
	}
}

// The reason this function exists: a cluster where policies are accepted and nothing is
// enforced is indistinguishable from one where isolation works.
func TestCNIDetection(t *testing.T) {
	cases := []struct {
		name      string
		daemons   []string
		enforces  bool
		known     bool
		mustMatch string
	}{
		{"calico", []string{"calico-node", "kube-proxy"}, true, true, "calico"},
		{"cilium", []string{"cilium"}, true, true, "cilium"},
		{"flannel", []string{"kube-flannel-ds", "kube-proxy"}, false, true, "flannel"},
		// The dangerous case, spelled out: the answer must be "not enforced", not "unknown".
		{"flannel alongside something else", []string{"kube-flannel-ds", "node-exporter"}, false, true, "enforces none"},
		{"nothing recognisable", []string{"node-exporter", "kube-proxy"}, false, false, "could not identify"},
		{"no daemonsets at all", nil, false, false, "could not identify"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enforces, known, note := CNIEnforcesNetworkPolicy(tc.daemons)
			if enforces != tc.enforces || known != tc.known {
				t.Errorf("= (enforces %v, known %v), want (%v, %v): %s",
					enforces, known, tc.enforces, tc.known, note)
			}
			if note == "" {
				t.Error("no note; the whole point is to say what was concluded and why")
			}
			if tc.mustMatch != "" && !strings.Contains(note, tc.mustMatch) {
				t.Errorf("note %q does not mention %q", note, tc.mustMatch)
			}
		})
	}
}
