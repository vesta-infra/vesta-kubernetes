package controllers

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The log collector, created on demand.
//
// It used to be rendered only by the chart, behind logging.enabled, which defaults to false.
// So creating a drain through the UI wrote a configuration and produced no collector to run
// it, and the only way to finish the job was a helm upgrade from a terminal — for a feature
// whose entire setup is otherwise in the browser.
//
// The operator is the only thing that knows whether a drain exists, so it owns the
// DaemonSet. Everything cluster-scoped stays in the chart: the ServiceAccount, the
// ClusterRole that lets Fluent Bit read pod metadata, and its binding. That is deliberate
// and not just tidiness — creating a ClusterRole requires the power to grant every
// permission in it, and an operator that can mint cluster roles is a much larger thing to
// trust than one that can run a DaemonSet.

// CollectorOptions are the knobs the chart used to supply through values.
type CollectorOptions struct {
	Image             string
	PullPolicy        corev1.PullPolicy
	PriorityClassName string
	Resources         corev1.ResourceRequirements
	// SecretRefs become environment variables, for drains whose credentials live in a
	// Secret rather than in the drain spec.
	SecretRefs []CollectorSecretRef
}

type CollectorSecretRef struct{ Env, Secret, Key string }

// DefaultCollectorImage is used when the chart supplies nothing, so a collector created on
// demand does not depend on a value the install may never have set.
const DefaultCollectorImage = "cr.fluentbit.io/fluent/fluent-bit:3.1.9"

// BuildCollectorDaemonSet renders the collector.
//
// Pure, so the shape can be tested without a cluster — which matters because the parts that
// are easy to get wrong here are the ones that fail quietly: a writable /var/log lets a
// logging agent corrupt what every other agent on the node reads, and a missing toleration
// means logs stop exactly when a node is draining, which is when they are worth having.
func BuildCollectorDaemonSet(namespace, checksum string, opts CollectorOptions) *appsv1.DaemonSet {
	image := opts.Image
	if image == "" {
		image = DefaultCollectorImage
	}
	pullPolicy := opts.PullPolicy
	if pullPolicy == "" {
		pullPolicy = corev1.PullIfNotPresent
	}

	resources := opts.Resources
	if resources.Requests == nil && resources.Limits == nil {
		// Small on purpose: a log shipper that outweighs what it ships is its own problem,
		// and this runs on every node.
		resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		}
	}

	env := make([]corev1.EnvVar, 0, len(opts.SecretRefs))
	for _, ref := range opts.SecretRefs {
		env = append(env, corev1.EnvVar{
			Name: ref.Env,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Secret},
					Key:                  ref.Key,
				},
			},
		})
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       collectorName,
		"app.kubernetes.io/component":  "logs",
		"app.kubernetes.io/managed-by": "vesta-operator",
	}

	hostPath := func(path string) corev1.VolumeSource {
		return corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: path}}
	}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      collectorName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": collectorName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{"checksum/config": checksum},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: collectorName,
					// Logs must keep flowing while a node is draining or tainted, which is
					// exactly when the interesting ones are produced.
					Tolerations:                   []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					PriorityClassName:             opts.PriorityClassName,
					TerminationGracePeriodSeconds: ptr.To(int64(30)),
					Containers: []corev1.Container{{
						Name:            "fluent-bit",
						Image:           image,
						ImagePullPolicy: pullPolicy,
						Ports:           []corev1.ContainerPort{{Name: "metrics", ContainerPort: 2020}},
						Env:             env,
						Resources:       resources,
						SecurityContext: &corev1.SecurityContext{
							// The container log files on the node are root-owned, so reading
							// them needs root. Everything else is dropped.
							RunAsUser:                ptr.To(int64(0)),
							ReadOnlyRootFilesystem:   ptr.To(true),
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/fluent-bit/etc/"},
							// Read-only, and it must stay that way: an agent that can write
							// to the node's log directory can corrupt what every other agent
							// reads.
							{Name: "varlog", MountPath: "/var/log", ReadOnly: true},
							{Name: "varlibdockercontainers", MountPath: "/var/lib/docker/containers", ReadOnly: true},
							// Top level on purpose, NOT under /var/log. That directory is a
							// read-only bind mount from the host, and runc cannot create a
							// mountpoint inside one -- the container fails to start with
							// "mkdirat ... read-only file system" before Fluent Bit runs at
							// all. The buffer holds the filesystem queue and the tail
							// position database, so it cannot simply be dropped: losing it
							// re-ships every log file from the beginning after a restart.
							{Name: "storage", MountPath: "/flb-storage"},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path: "/", Port: intstr.FromInt32(2020)}},
							InitialDelaySeconds: 10,
							PeriodSeconds:       30,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/health", Port: intstr.FromInt32(2020)}},
							InitialDelaySeconds: 5,
							PeriodSeconds:       10,
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: collectorConfigMap},
								Optional:             ptr.To(true),
							}},
						},
						{Name: "varlog", VolumeSource: hostPath("/var/log")},
						{Name: "varlibdockercontainers", VolumeSource: hostPath("/var/lib/docker/containers")},
						{Name: "storage", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

// collectorOptions reads the chart's settings off the DaemonSet the chart rendered, when
// there is one, and otherwise falls back to the defaults above.
//
// Deliberately not read from VestaConfig: these are deployment details of a component the
// chart owns the identity of, and duplicating them into a CRD would give two places to set
// one thing.
func (r *VestaLogDrainReconciler) collectorOptions(env []corev1.EnvVar) CollectorOptions {
	opts := CollectorOptions{}
	for _, e := range env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			opts.SecretRefs = append(opts.SecretRefs, CollectorSecretRef{
				Env:    e.Name,
				Secret: e.ValueFrom.SecretKeyRef.Name,
				Key:    e.ValueFrom.SecretKeyRef.Key,
			})
		}
	}
	return opts
}

// removeOwnCollector deletes the DaemonSet, but only one this operator created.
//
// The managed-by label is the whole check. A collector the chart rendered is Helm's, and
// deleting it here would start a fight: Helm puts it back on the next upgrade, this puts it
// away on the next reconcile, and the logs stop and start with nothing explaining why.
func (r *VestaLogDrainReconciler) removeOwnCollector(ctx context.Context) error {
	ds := &appsv1.DaemonSet{}
	err := r.Get(ctx, client.ObjectKey{Namespace: drainHomeNamespace, Name: collectorName}, ds)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if ds.Labels["app.kubernetes.io/managed-by"] != "vesta-operator" {
		return nil
	}
	return r.Delete(ctx, ds)
}
