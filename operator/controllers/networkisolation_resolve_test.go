package controllers

import (
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func isoPtr(b bool) *bool { return &b }

// Isolation was platform-wide and nothing else. Turning it on for the one project holding
// something sensitive meant turning it on for every project, which is a far larger change
// than the need justifies — so in practice it stayed off.
func TestProjectCanIsolateWithoutTheInstance(t *testing.T) {
	got := ResolveNetworkIsolation(
		nil,
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(true)},
		nil,
	)
	if !got.Enabled {
		t.Error("a project asking for isolation did not get it")
	}
}

// And the reverse: a project must be able to opt out of a platform that has it on. This is
// why Enabled is a pointer — with a plain bool, "off" and "not set" are the same value and
// the platform default would always win.
func TestProjectCanOptOutOfPlatformIsolation(t *testing.T) {
	got := ResolveNetworkIsolation(
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(true)},
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(false)},
		nil,
	)
	if got.Enabled {
		t.Error("a project could not exempt itself from platform-wide isolation")
	}
}

// Saying nothing must inherit, not override with a zero value.
func TestSilentLayersInherit(t *testing.T) {
	platform := &vestav1alpha1.NetworkIsolationConfig{
		Enabled:           isoPtr(true),
		TrustedNamespaces: []string{"traefik", "kube-system"},
		MetricsPort:       9090,
	}

	// A project that only names trusted namespaces keeps the platform's enabled and port.
	got := ResolveNetworkIsolation(platform,
		&vestav1alpha1.NetworkIsolationConfig{TrustedNamespaces: []string{"my-ingress"}}, nil)

	if !got.Enabled {
		t.Error("a project that said nothing about enabled turned isolation off")
	}
	if got.MetricsPort != 9090 {
		t.Errorf("metrics port = %d, want the platform's 9090", got.MetricsPort)
	}
	if len(got.TrustedNamespaces) != 1 || got.TrustedNamespaces[0] != "my-ingress" {
		t.Errorf("trusted = %v; a layer naming its list states the whole list, and merging "+
			"would silently trust more than it asked for", got.TrustedNamespaces)
	}
}

// The environment is the narrowest layer and wins over both.
func TestEnvironmentIsNarrowest(t *testing.T) {
	platform := &vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(false)}
	project := &vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(false)}
	env := &vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(true)}

	if !ResolveNetworkIsolation(platform, project, env).Enabled {
		t.Error("an environment asking for isolation was overruled by layers above it")
	}
	if ResolveNetworkIsolation(
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(true)},
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(true)},
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(false)},
	).Enabled {
		t.Error("an environment could not exempt itself")
	}
}

// Nothing configured anywhere must leave isolation off. This is the state of every existing
// install, and enabling it by accident would deny inbound traffic across the instance.
func TestNothingConfiguredIsOff(t *testing.T) {
	got := ResolveNetworkIsolation(nil, nil, nil)
	if got.Enabled {
		t.Fatal("isolation defaulted to on with nothing configured; every environment would " +
			"start denying traffic")
	}
	if len(BuildNetworkPolicies("acme-prod", got)) != 0 {
		t.Error("policies were rendered for an unconfigured environment")
	}
}

// Environments of an isolated project do not reach each other: the decision recorded for
// this feature, and the reason it is useful for a project holding something sensitive.
func TestIsolatedProjectEnvironmentsStayApartFromEachOther(t *testing.T) {
	cfg := ResolveNetworkIsolation(nil,
		&vestav1alpha1.NetworkIsolationConfig{Enabled: isoPtr(true)}, nil)

	policies := BuildNetworkPolicies("acme-staging", cfg)

	for _, p := range policies {
		for _, rule := range p.Spec.Ingress {
			for _, peer := range rule.From {
				if peer.NamespaceSelector == nil {
					continue
				}
				// Only the trusted list may name namespaces. Anything selecting the
				// project's other environments would let staging reach production.
				for _, req := range peer.NamespaceSelector.MatchExpressions {
					for _, v := range req.Values {
						if v == "acme-production" {
							t.Error("a sibling environment is allowed in; staging could reach " +
								"production inside the same project")
						}
					}
				}
			}
		}
	}
}
