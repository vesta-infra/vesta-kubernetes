package controllers

import (
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Network isolation between environments.
//
// Two things about NetworkPolicy decide the shape of everything here.
//
// The first is that it is not enforced by Kubernetes. It is enforced by the CNI plugin, and a
// cluster running one that does not implement it -- flannel is the common case -- accepts
// every policy, shows them in kubectl, and enforces none of them. That is worse than having
// no policies at all, because it is indistinguishable from working. CNIEnforcesNetworkPolicy
// below exists to say so out loud rather than let an operator believe otherwise.
//
// The second is that a default-deny policy is subtractive and unforgiving: the moment one
// exists for a pod, only what is explicitly allowed gets through. Which is why this does
// INGRESS only and never egress. Egress deny is where clusters break -- DNS stops resolving,
// and every symptom afterwards points somewhere else entirely -- and the isolation that was
// actually asked for, keeping environments away from each other, is entirely an ingress
// property.

const (
	policyDenyIngress   = "vesta-default-deny-ingress"
	policySameNamespace = "vesta-allow-same-namespace"
	policyFromTrusted   = "vesta-allow-trusted-namespaces"
)

// NetworkIsolation is the resolved configuration for one namespace.
type NetworkIsolation struct {
	Enabled bool
	// TrustedNamespaces may reach apps in this namespace: the ingress controller, and
	// whatever scrapes metrics. Matched by the standard kubernetes.io/metadata.name label,
	// which every namespace carries automatically.
	TrustedNamespaces []string
	// TrustedNamespaceLabels is the alternative for clusters that label their namespaces by
	// purpose rather than naming them predictably.
	TrustedNamespaceLabels map[string]string
	// MetricsPort is left reachable from anywhere when set, because a scraper that cannot
	// be located by namespace is common and a closed metrics port fails silently -- the
	// dashboard simply goes blank.
	MetricsPort int32
}

// defaultTrustedNamespaces is where ingress controllers and monitoring usually live.
//
// A list of guesses, and it is only a default: naming a namespace that does not exist is
// harmless, because a namespace selector that matches nothing allows nothing.
var defaultTrustedNamespaces = []string{
	"kube-system",
	"traefik",
	"ingress-nginx",
	"monitoring",
	"vesta-system",
}

// ResolveNetworkIsolation merges the three levels, narrowest last.
//
// Field by field rather than whole-object, the same way ResolveQuota does it, so a project
// can switch isolation on without restating the platform's trusted-namespace list — and an
// environment can be exempted from a project that has it on.
//
// The semantics do not change with the level: an isolated namespace denies inbound traffic
// from everywhere except itself and the trusted namespaces. Environments of the same
// project do not reach each other, which is what makes this worth having for a project
// holding something sensitive.
func ResolveNetworkIsolation(platform, project, env *vestav1alpha1.NetworkIsolationConfig) NetworkIsolation {
	var out NetworkIsolation

	for _, layer := range []*vestav1alpha1.NetworkIsolationConfig{platform, project, env} {
		if layer == nil {
			continue
		}
		// A nil Enabled means the layer says nothing about it; false means it says no.
		if layer.Enabled != nil {
			out.Enabled = *layer.Enabled
		}
		if len(layer.TrustedNamespaces) > 0 {
			// Replaces rather than appends: a layer naming its trusted namespaces is
			// stating the whole list, and merging would silently trust more than it asked.
			out.TrustedNamespaces = layer.TrustedNamespaces
		}
		if len(layer.TrustedNamespaceLabels) > 0 {
			out.TrustedNamespaceLabels = layer.TrustedNamespaceLabels
		}
		if layer.MetricsPort > 0 {
			out.MetricsPort = layer.MetricsPort
		}
	}
	return out
}

// BuildNetworkPolicies renders the policies for one namespace.
//
// Returns nothing when isolation is off, so turning it off removes the policies rather than
// leaving a deny in place that nothing will ever clean up.
func BuildNetworkPolicies(namespace string, cfg NetworkIsolation) []*networkingv1.NetworkPolicy {
	if !cfg.Enabled {
		return nil
	}

	labels := map[string]string{"app.kubernetes.io/managed-by": "vesta-operator"}
	meta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}
	}

	policies := []*networkingv1.NetworkPolicy{
		// Deny everything inbound. Ingress only: policyTypes omits Egress deliberately, so
		// outbound traffic and DNS are untouched.
		{
			ObjectMeta: meta(policyDenyIngress),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			},
		},
		// Allow the namespace to talk to itself. An app reaching its own database, and the
		// activator proxying a request to the app it just woke, are both this rule -- without
		// it, turning isolation on breaks scale-to-zero.
		{
			ObjectMeta: meta(policySameNamespace),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{{
						PodSelector: &metav1.LabelSelector{},
					}},
				}},
			},
		},
	}

	if from := trustedPeers(cfg); len(from) > 0 {
		policies = append(policies, &networkingv1.NetworkPolicy{
			ObjectMeta: meta(policyFromTrusted),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: from}},
			},
		})
	}

	if cfg.MetricsPort > 0 {
		port := intstr.FromInt32(cfg.MetricsPort)
		policies = append(policies, &networkingv1.NetworkPolicy{
			ObjectMeta: meta("vesta-allow-metrics"),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				// An empty From with a port means "from anywhere, on this port only".
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					Ports: []networkingv1.NetworkPolicyPort{{Port: &port}},
				}},
			},
		})
	}

	return policies
}

// trustedPeers turns the configured namespaces and labels into selectors.
func trustedPeers(cfg NetworkIsolation) []networkingv1.NetworkPolicyPeer {
	names := cfg.TrustedNamespaces
	if len(names) == 0 && len(cfg.TrustedNamespaceLabels) == 0 {
		names = defaultTrustedNamespaces
	}

	var peers []networkingv1.NetworkPolicyPeer

	if len(names) > 0 {
		sorted := append([]string(nil), names...)
		sort.Strings(sorted)
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					// Set automatically on every namespace since 1.21, so this needs no
					// cooperation from whoever created the namespace.
					Key:      "kubernetes.io/metadata.name",
					Operator: metav1.LabelSelectorOpIn,
					Values:   sorted,
				}},
			},
		})
	}

	if len(cfg.TrustedNamespaceLabels) > 0 {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: cfg.TrustedNamespaceLabels},
		})
	}

	return peers
}

// enforcingCNIs are the plugins known to implement NetworkPolicy, matched against the names
// of DaemonSets running in the cluster.
var enforcingCNIs = []string{"calico", "cilium", "weave", "antrea", "kube-router", "ovn-kubernetes", "kube-ovn"}

// nonEnforcingCNIs accept every policy and enforce none.
var nonEnforcingCNIs = []string{"flannel", "kube-flannel"}

// CNIEnforcesNetworkPolicy guesses whether policies will actually be enforced.
//
// Pure, over the names of DaemonSets in the cluster, because the alternative -- letting an
// operator turn on isolation, see the policies created, and believe their environments are
// separated when nothing is filtering a single packet -- is the worst failure this feature
// has available to it.
//
// A guess, and it says so: an unrecognised CNI returns unknown rather than either answer.
func CNIEnforcesNetworkPolicy(daemonSetNames []string) (enforces bool, known bool, note string) {
	var found []string
	for _, name := range daemonSetNames {
		lower := strings.ToLower(name)
		for _, bad := range nonEnforcingCNIs {
			if strings.Contains(lower, bad) {
				return false, true, "this cluster appears to run flannel, which accepts NetworkPolicy " +
					"objects and enforces none of them; the policies will exist but nothing will be isolated"
			}
		}
		for _, good := range enforcingCNIs {
			if strings.Contains(lower, good) {
				found = append(found, good)
			}
		}
	}

	if len(found) > 0 {
		return true, true, "enforced by " + strings.Join(found, ", ")
	}
	return false, false, "could not identify this cluster's CNI; NetworkPolicy is enforced by the " +
		"network plugin, not by Kubernetes, so verify that yours implements it before relying on this"
}
