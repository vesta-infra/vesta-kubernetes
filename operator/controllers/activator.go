package controllers

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Wake-on-traffic.
//
// A sleeping app has no pods, so its Service resolves to nothing and every request fails.
// The activator takes the app's place in the ingress while it is down: it holds the request,
// wakes the app by patching spec.desiredState, waits for a pod to become ready, and proxies
// through. The requester sees a slow response rather than an error.
//
// It runs per namespace rather than once in vesta-system because an Ingress backend must
// name a Service in its own namespace. Routing across namespaces would mean an ExternalName
// backend, which both Traefik and nginx refuse unless explicitly enabled -- and the symptom
// of that refusal is a 404 with nothing in any log to explain it.

const (
	activatorName = "vesta-activator"
	activatorPort = 8080
	// The probe listens separately, so no application path is reserved by the activator.
	activatorProbePort = 8081
	activatorAppLabel  = "kubernetes.getvesta.sh/activator"
)

// ingressBackend decides which Service an app's ingress points at.
//
// The rule is keyed on readiness, not on the desired state alone. Flipping back the moment
// somebody asks for the app to run would point the ingress at a Service with no ready
// endpoints behind it -- so the first requests after a wake would fail, which is exactly the
// window the activator exists to cover.
func ingressBackend(appName string, atRest bool, readyReplicas int32, wakeOnTraffic bool) string {
	if !wakeOnTraffic {
		// Without the activator there is nothing else to point at. A sleeping app's
		// requests fail, which is the honest behaviour when nothing can wake it.
		return appName
	}
	if atRest || readyReplicas == 0 {
		return activatorName
	}
	return appName
}

// activatorNeeded reports whether a namespace needs an activator running.
//
// Only apps that opted into wake-on-traffic count. An activator in a namespace where nothing
// uses it is a pod doing nothing, which is a poor look for a feature whose entire purpose is
// not running pods.
func activatorNeeded(apps []vestav1alpha1.VestaApp, namespace, project string) bool {
	for i := range apps {
		app := &apps[i]
		if app.Spec.Project != project {
			continue
		}
		if app.Spec.Sleep.WakeOnTrafficEnabled() {
			return true
		}
	}
	return false
}

// buildActivatorDeployment renders the per-namespace activator.
func buildActivatorDeployment(namespace, image string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{
		"app.kubernetes.io/name":       activatorName,
		"app.kubernetes.io/managed-by": "vesta-operator",
		activatorAppLabel:              "true",
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: activatorName, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{activatorAppLabel: "true"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: activatorName,
					Containers: []corev1.Container{{
						Name:  "activator",
						Image: image,
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: activatorPort},
							{Name: "probe", ContainerPort: activatorProbePort},
						},
						Env: []corev1.EnvVar{
							{Name: "VESTA_ACTIVATOR_NAMESPACE", Value: namespace},
						},
						// Small on purpose. An activator that costs more than the app it
						// is standing in for defeats the point of scaling to zero.
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    mustQuantity("10m"),
								corev1.ResourceMemory: mustQuantity("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    mustQuantity("200m"),
								corev1.ResourceMemory: mustQuantity("128Mi"),
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz", Port: intstr.FromInt32(activatorProbePort),
								},
							},
							InitialDelaySeconds: 2,
							PeriodSeconds:       10,
						},
					}},
				},
			},
		},
	}
}

// buildActivatorService renders the Service an ingress points at while an app is down.
func buildActivatorService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      activatorName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vesta-operator",
				activatorAppLabel:              "true",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{activatorAppLabel: "true"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: activatorPort, TargetPort: intstr.FromInt32(activatorPort),
			}},
		},
	}
}

func mustQuantity(s string) resource.Quantity {
	return resource.MustParse(s)
}

// buildActivatorServiceAccount is the identity an activator runs as.
//
// One per namespace, because a pod can only use a ServiceAccount from its own namespace.
func buildActivatorServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      activatorName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vesta-operator",
				activatorAppLabel:              "true",
			},
		},
	}
}

// buildActivatorRoleBinding binds an activator's ServiceAccount to one of the chart's
// ClusterRoles, in the namespace the permissions should apply to.
//
// A RoleBinding rather than a ClusterRoleBinding on purpose: binding a ClusterRole with a
// RoleBinding grants it only within that one namespace, so an activator can read routing in
// its own namespace and nothing else. The wake permission is bound the same way in
// vesta-system, which is where every VestaApp lives regardless of where its pods run.
func buildActivatorRoleBinding(name, namespace, clusterRole, subjectNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vesta-operator",
				activatorAppLabel:              "true",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRole,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      activatorName,
			Namespace: subjectNamespace,
		}},
	}
}

// activatorWakeBindingName is the binding in vesta-system for one namespace's activator.
// Named after the namespace because there is one per activator and they share a namespace.
func activatorWakeBindingName(namespace string) string {
	return activatorName + "-wake-" + namespace
}
