package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// Configuring a drain in the UI produced no collector: the DaemonSet was rendered only by
// the chart, behind logging.enabled, which defaults to false. The operator is the only thing
// that knows a drain exists, so it builds one now — and the details below are the ones that
// fail quietly if they are wrong.
func TestCollectorDaemonSetShape(t *testing.T) {
	ds := BuildCollectorDaemonSet("vesta-system", "abc123", CollectorOptions{})

	pod := ds.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]

	// Logs must keep flowing while a node is draining or tainted, which is exactly when the
	// interesting ones are produced.
	if len(pod.Tolerations) == 0 || pod.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Error("no blanket toleration; logs would stop on a tainted node")
	}

	// An agent that can write to the node's log directory can corrupt what every other
	// agent on that node reads.
	var varlog *corev1.VolumeMount
	for i := range c.VolumeMounts {
		if c.VolumeMounts[i].MountPath == "/var/log" {
			varlog = &c.VolumeMounts[i]
		}
	}
	if varlog == nil {
		t.Fatal("/var/log is not mounted; there is nothing to read")
	}
	if !varlog.ReadOnly {
		t.Error("/var/log is mounted writable")
	}

	// Reading root-owned container logs needs root; everything else goes.
	sc := c.SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 0 {
		t.Error("not running as root; the container log files on the node are root-owned")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("privilege escalation is allowed")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("root filesystem is writable")
	}

	// The config arrives from a ConfigMap the operator writes separately, and the DaemonSet
	// must tolerate it not existing yet or the pod blocks on a volume that is moments away.
	var cfg *corev1.Volume
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == "config" {
			cfg = &pod.Volumes[i]
		}
	}
	if cfg == nil || cfg.ConfigMap == nil {
		t.Fatal("no config volume")
	}
	if cfg.ConfigMap.Optional == nil || !*cfg.ConfigMap.Optional {
		t.Error("the config volume is not optional; the pod would not start before the " +
			"operator had written the ConfigMap")
	}

	// The checksum is what makes a drain change actually roll the pods.
	if ds.Spec.Template.Annotations["checksum/config"] != "abc123" {
		t.Error("the config checksum is not on the pod template, so editing a drain would " +
			"rewrite the ConfigMap and leave every pod running the old configuration")
	}

	if ds.Labels["app.kubernetes.io/managed-by"] != "vesta-operator" {
		t.Error("not labelled as operator-managed; teardown checks that label before " +
			"deleting, so an unlabelled one would never be cleaned up")
	}
}

// An install that never set logging.image gets a working collector rather than a DaemonSet
// pointing at an empty image.
func TestCollectorHasAnImageWithoutChartValues(t *testing.T) {
	ds := BuildCollectorDaemonSet("vesta-system", "x", CollectorOptions{})
	if img := ds.Spec.Template.Spec.Containers[0].Image; img == "" || img == ":" {
		t.Errorf("image = %q, want a usable default", img)
	}
	if ds.Spec.Template.Spec.Containers[0].Resources.Requests == nil {
		t.Error("no resource requests; this runs on every node and would be unschedulable " +
			"under a namespace quota that requires them")
	}
}

// Credentials reach the collector as environment variables, which is how a drain whose
// token lives in a Secret authenticates.
func TestCollectorCarriesSecretBackedCredentials(t *testing.T) {
	ds := BuildCollectorDaemonSet("vesta-system", "x", CollectorOptions{
		SecretRefs: []CollectorSecretRef{{Env: "OO_PASSWORD", Secret: "oo", Key: "password"}},
	})

	env := ds.Spec.Template.Spec.Containers[0].Env
	if len(env) != 1 || env[0].Name != "OO_PASSWORD" {
		t.Fatalf("env = %+v, want the secret-backed variable", env)
	}
	if env[0].ValueFrom == nil || env[0].ValueFrom.SecretKeyRef == nil {
		t.Error("the credential was inlined rather than referenced from its Secret")
	}
	if env[0].Value != "" {
		t.Error("a credential value is set literally on the pod spec")
	}
}
