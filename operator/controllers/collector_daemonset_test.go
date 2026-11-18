package controllers

import (
	"os"
	"strings"
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

// The collector would not start at all:
//
//	error mounting ".../volumes/kubernetes.io~empty-dir/storage" to rootfs at
//	"/var/log/flb-storage": create mountpoint: mkdirat ...: read-only file system
//
// /var/log is a read-only bind mount from the host, and runc cannot create a mountpoint
// inside one. The buffer was nested there, so the container died before Fluent Bit ran.
//
// Both mounts are necessary and both must stay as they are — /var/log read-only because an
// agent that can write to the node's log directory corrupts what every other agent reads,
// and the buffer writable because it holds the filesystem queue and the tail position
// database. They simply cannot be nested.
func TestWritableMountsAreNotInsideReadOnlyOnes(t *testing.T) {
	ds := BuildCollectorDaemonSet("vesta-system", "x", CollectorOptions{})
	mounts := ds.Spec.Template.Spec.Containers[0].VolumeMounts

	for _, w := range mounts {
		if w.ReadOnly {
			continue
		}
		for _, ro := range mounts {
			if !ro.ReadOnly || ro.MountPath == w.MountPath {
				continue
			}
			if strings.HasPrefix(w.MountPath, strings.TrimSuffix(ro.MountPath, "/")+"/") {
				t.Errorf("writable mount %q is inside read-only mount %q; the container "+
					"cannot start, because runc must create the mountpoint and cannot write "+
					"there", w.MountPath, ro.MountPath)
			}
		}
	}
}

// The buffer path in the DaemonSet and the one in the generated configuration are set in
// different files and nothing connects them. A mismatch is not a startup failure — the
// container comes up and Fluent Bit writes its queue into the container's own filesystem,
// which is read-only, so it degrades to memory buffering and loses its tail position on
// every restart.
func TestBufferPathMatchesTheGeneratedConfig(t *testing.T) {
	ds := BuildCollectorDaemonSet("vesta-system", "x", CollectorOptions{})

	var buffer string
	for _, m := range ds.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "storage" {
			buffer = m.MountPath
		}
	}
	if buffer == "" {
		t.Fatal("no buffer volume is mounted")
	}

	raw, err := os.ReadFile("fluentbit_config.go")
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)

	for _, setting := range []string{"storage.path", "DB "} {
		i := strings.Index(config, setting)
		if i < 0 {
			t.Errorf("the generated config sets no %s", strings.TrimSpace(setting))
			continue
		}
		line := config[i:]
		if end := strings.Index(line, "\n"); end > 0 {
			line = line[:end]
		}
		if !strings.Contains(line, buffer) {
			t.Errorf("%s points outside the mounted buffer %q: %s",
				strings.TrimSpace(setting), buffer, strings.TrimSpace(line))
		}
	}
}

// Creating the collector was not enough: rollCollector only ever updated the checksum
// annotation and the environment, so a DaemonSet kept whatever spec it was first created
// with. The buffer mounted inside a read-only /var/log stopped the container from starting,
// and shipping the fix repaired nothing — no code path looked at the mounts again, so the
// broken DaemonSet sat there until somebody deleted it by hand.
//
// A collector this operator owns has to have its whole spec reconciled.
func TestOperatorOwnedCollectorHasItsSpecReconciled(t *testing.T) {
	src, err := os.ReadFile("vestalogdrain_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	roll := string(src)
	i := strings.Index(roll, "func (r *VestaLogDrainReconciler) rollCollector")
	if i < 0 {
		t.Fatal("rollCollector not found")
	}
	body := roll[i:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	if !strings.Contains(body, "BuildCollectorDaemonSet") {
		t.Error("rollCollector never rebuilds the desired spec, so a collector created with a " +
			"broken one is never repaired")
	}
	if !strings.Contains(body, "ds.Spec = desired.Spec") {
		t.Error("the desired spec is built but not assigned")
	}

	// And it must only do that for its own: rewriting a chart-rendered DaemonSet starts a
	// fight where Helm restores it on upgrade and this rewrites it on the next reconcile.
	if !strings.Contains(body, `ds.Labels["app.kubernetes.io/managed-by"] == "vesta-operator"`) {
		t.Error("the spec is reconciled without checking ownership; a chart-rendered " +
			"collector would be fought over with Helm")
	}
}
