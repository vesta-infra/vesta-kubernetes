package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

type VestaAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// targetEnv holds the resolved namespace and per-environment configuration
type targetEnv struct {
	Namespace string
	Config    vestav1alpha1.AppEnvironmentConfig
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create

func (r *VestaAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var app vestav1alpha1.VestaApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling VestaApp", "name", app.Name, "project", app.Spec.Project)

	if app.Labels == nil {
		app.Labels = map[string]string{}
	}
	app.Labels["kubernetes.getvesta.sh/project"] = app.Spec.Project
	app.Labels["kubernetes.getvesta.sh/app"] = app.Name
	if err := r.Update(ctx, &app); err != nil {
		return ctrl.Result{}, err
	}

	targetNamespaces, err := r.resolveTargetNamespaces(ctx, &app)
	if err != nil {
		return r.updateStatusFailed(ctx, &app, fmt.Errorf("resolve target namespaces: %w", err))
	}

	// Fetch project to get inherited labels/annotations
	var project vestav1alpha1.VestaProject
	projectLabels := map[string]string{}
	projectAnnotations := map[string]string{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Project}, &project); err == nil {
		projectLabels = project.Spec.Labels
		projectAnnotations = project.Spec.Annotations
	}

	if len(targetNamespaces) == 0 {
		logger.Info("no target namespaces resolved, skipping resource reconciliation")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	for _, target := range targetNamespaces {
		if err := r.ensureNamespace(ctx, target.Namespace); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if err := r.reconcileDeployment(ctx, &app, target, projectLabels, projectAnnotations); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if err := r.reconcileService(ctx, &app, target.Namespace); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if app.Spec.Ingress != nil {
			if err := r.reconcileIngress(ctx, &app, target.Namespace); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
		}

		if target.Config.Autoscale != nil && target.Config.Autoscale.Enabled {
			if err := r.reconcileHPA(ctx, &app, target); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
		}
	}

	if err := r.updateStatusRunning(ctx, &app); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// resolveTargetNamespaces determines which {project}-{app}-{env} namespaces to deploy
// into. If spec.environments is set, only those environments are targeted.
// Otherwise all environments for the project are used with default config.
func (r *VestaAppReconciler) resolveTargetNamespaces(ctx context.Context, app *vestav1alpha1.VestaApp) ([]targetEnv, error) {
	if len(app.Spec.Environments) > 0 {
		targets := make([]targetEnv, 0, len(app.Spec.Environments))
		for _, env := range app.Spec.Environments {
			targets = append(targets, targetEnv{
				Namespace: fmt.Sprintf("%s-%s-%s", app.Spec.Project, app.Name, env.Name),
				Config:    env,
			})
		}
		return targets, nil
	}

	var envList vestav1alpha1.VestaEnvironmentList
	if err := r.List(ctx, &envList, client.MatchingLabels{
		"kubernetes.getvesta.sh/project": app.Spec.Project,
	}); err != nil {
		return nil, fmt.Errorf("list environments for project %s: %w", app.Spec.Project, err)
	}

	targets := make([]targetEnv, 0, len(envList.Items))
	for _, env := range envList.Items {
		targets = append(targets, targetEnv{
			Namespace: fmt.Sprintf("%s-%s-%s", app.Spec.Project, app.Name, env.Name),
			Config:    vestav1alpha1.AppEnvironmentConfig{Name: env.Name},
		})
	}
	return targets, nil
}

func (r *VestaAppReconciler) ensureNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := r.Create(ctx, ns); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("create namespace %s: %w", name, err)
		}
	}
	return nil
}

func (r *VestaAppReconciler) reconcileDeployment(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv, projectLabels, projectAnnotations map[string]string) error {
	labels := r.labelsForApp(app)
	replicas := int32(1)
	if target.Config.Replicas != nil {
		replicas = *target.Config.Replicas
	}

	container := r.buildContainer(app)
	podSpec := r.buildPodSpec(app, container)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: target.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		deploy.Labels = labels
		// Apply project-level labels first, then app-level overrides
		for k, v := range projectLabels {
			deploy.Labels[k] = v
		}
		if app.Spec.CustomConfig != nil {
			for k, v := range app.Spec.CustomConfig.Labels {
				deploy.Labels[k] = v
			}
		}
		deploy.Annotations = map[string]string{}
		// Apply project-level annotations first, then app-level overrides
		for k, v := range projectAnnotations {
			deploy.Annotations[k] = v
		}
		if app.Spec.CustomConfig != nil {
			for k, v := range app.Spec.CustomConfig.Annotations {
				deploy.Annotations[k] = v
			}
		}

		deploy.Spec = appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("reconcile deployment in %s: %w", target.Namespace, err)
	}

	log.FromContext(ctx).Info("deployment reconciled", "namespace", target.Namespace, "operation", op)
	return nil
}

func (r *VestaAppReconciler) buildContainer(app *vestav1alpha1.VestaApp) corev1.Container {
	image := "placeholder:latest"
	if app.Spec.Image != nil {
		tag := "latest"
		if app.Spec.Image.Tag != "" {
			tag = app.Spec.Image.Tag
		}
		image = fmt.Sprintf("%s:%s", app.Spec.Image.Repository, tag)
	}

	container := corev1.Container{
		Name:  "app",
		Image: image,
	}

	if app.Spec.Image != nil && app.Spec.Image.PullPolicy != "" {
		container.ImagePullPolicy = app.Spec.Image.PullPolicy
	}

	if app.Spec.Runtime.Port > 0 {
		container.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: app.Spec.Runtime.Port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}

	if app.Spec.Runtime.Command != "" {
		container.Command = []string{"/bin/sh", "-c", app.Spec.Runtime.Command}
	}
	if len(app.Spec.Runtime.Args) > 0 {
		container.Args = app.Spec.Runtime.Args
	}

	container.Env = append(container.Env, app.Spec.Runtime.Env...)

	for _, sb := range app.Spec.Runtime.Secrets {
		if sb.SecretRef != nil {
			if len(sb.Keys) > 0 {
				for _, km := range sb.Keys {
					container.Env = append(container.Env, corev1.EnvVar{
						Name: km.EnvVar,
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: sb.SecretRef.Name},
								Key:                  km.SecretKey,
							},
						},
					})
				}
			} else {
				container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: sb.SecretRef.Name},
					},
				})
			}
		}
		if sb.SecretKeyRef != nil {
			container.Env = append(container.Env, corev1.EnvVar{
				Name: sb.SecretKeyRef.EnvVar,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: sb.SecretKeyRef.Name},
						Key:                  sb.SecretKeyRef.Key,
					},
				},
			})
		}
		if sb.SecretMount != nil {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      fmt.Sprintf("secret-%s", sb.SecretMount.Name),
				MountPath: sb.SecretMount.MountPath,
				ReadOnly:  sb.SecretMount.ReadOnly,
			})
		}
	}

	for _, v := range app.Spec.Runtime.Volumes {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
		})
	}

	if app.Spec.Resources != nil {
		if app.Spec.Resources.Requests != nil {
			container.Resources.Requests = app.Spec.Resources.Requests
		}
		if app.Spec.Resources.Limits != nil {
			container.Resources.Limits = app.Spec.Resources.Limits
		}
	}

	return container
}

func (r *VestaAppReconciler) buildPodSpec(app *vestav1alpha1.VestaApp, container corev1.Container) corev1.PodSpec {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
	}

	if app.Spec.Image != nil {
		for _, ips := range app.Spec.Image.ImagePullSecrets {
			podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: ips.Name})
		}
	}

	for _, sb := range app.Spec.Runtime.Secrets {
		if sb.SecretMount != nil {
			podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
				Name: fmt.Sprintf("secret-%s", sb.SecretMount.Name),
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: sb.SecretMount.Name,
					},
				},
			})
		}
	}

	for _, v := range app.Spec.Runtime.Volumes {
		if v.PersistentVolumeClaim != nil {
			podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
				Name: v.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: v.PersistentVolumeClaim.ClaimName,
					},
				},
			})
		}
	}

	return podSpec
}

func (r *VestaAppReconciler) reconcileService(ctx context.Context, app *vestav1alpha1.VestaApp, namespace string) error {
	if app.Spec.Runtime.Port == 0 {
		return nil
	}

	labels := r.labelsForApp(app)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = labels
		svc.Spec = corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       80,
					TargetPort: intstr.FromInt32(app.Spec.Runtime.Port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		}
		return nil
	})

	return err
}

func (r *VestaAppReconciler) reconcileIngress(ctx context.Context, app *vestav1alpha1.VestaApp, namespace string) error {
	labels := r.labelsForApp(app)
	pathType := networkingv1.PathTypePrefix

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		ing.Labels = labels
		ing.Annotations = map[string]string{}

		if app.Spec.Ingress.ClusterIssuer != "" {
			ing.Annotations["cert-manager.io/cluster-issuer"] = app.Spec.Ingress.ClusterIssuer
		}
		for k, v := range app.Spec.Ingress.Annotations {
			ing.Annotations[k] = v
		}

		ing.Spec = networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: app.Spec.Ingress.Domain,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: app.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		if app.Spec.Ingress.TLS {
			ing.Spec.TLS = []networkingv1.IngressTLS{
				{
					Hosts:      []string{app.Spec.Ingress.Domain},
					SecretName: fmt.Sprintf("%s-tls", app.Name),
				},
			}
		}

		return nil
	})

	return err
}

func (r *VestaAppReconciler) reconcileHPA(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv) error {
	as := target.Config.Autoscale
	labels := r.labelsForApp(app)

	// Ensure valid min/max replicas
	minReplicas := int32(1)
	if as.MinReplicas != nil && *as.MinReplicas > 0 {
		minReplicas = *as.MinReplicas
	}
	maxReplicas := as.MaxReplicas
	if maxReplicas < minReplicas {
		maxReplicas = minReplicas
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: target.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, hpa, func() error {
		hpa.Labels = labels
		hpa.Spec = autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       app.Name,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
		}

		// Build metrics from config
		hpa.Spec.Metrics = nil
		for _, m := range as.Metrics {
			switch m.Type {
			case "cpu":
				hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: m.TargetAverageUtilization,
						},
					},
				})
			case "memory":
				hpa.Spec.Metrics = append(hpa.Spec.Metrics, autoscalingv2.MetricSpec{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceMemory,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: m.TargetAverageUtilization,
						},
					},
				})
			}
		}

		// Default to 80% CPU if no metrics specified
		if len(hpa.Spec.Metrics) == 0 {
			defaultCPU := int32(80)
			hpa.Spec.Metrics = []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &defaultCPU,
						},
					},
				},
			}
		}

		if as.Behavior != nil {
			hpa.Spec.Behavior = as.Behavior
		}

		return nil
	})

	return err
}

func (r *VestaAppReconciler) updateStatusRunning(ctx context.Context, app *vestav1alpha1.VestaApp) error {
	app.Status.Phase = "Running"
	if app.Spec.Image != nil {
		app.Status.CurrentImage = fmt.Sprintf("%s:%s", app.Spec.Image.Repository, app.Spec.Image.Tag)
	}
	if app.Spec.Ingress != nil {
		scheme := "http"
		if app.Spec.Ingress.TLS {
			scheme = "https"
		}
		app.Status.URL = fmt.Sprintf("%s://%s", scheme, app.Spec.Ingress.Domain)
	}
	app.Status.LastDeployedAt = time.Now().UTC().Format(time.RFC3339)
	return r.Status().Update(ctx, app)
}

func (r *VestaAppReconciler) updateStatusFailed(ctx context.Context, app *vestav1alpha1.VestaApp, reconcileErr error) (ctrl.Result, error) {
	app.Status.Phase = "Failed"
	if err := r.Status().Update(ctx, app); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 1 * time.Minute}, reconcileErr
}

func (r *VestaAppReconciler) labelsForApp(app *vestav1alpha1.VestaApp) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       app.Name,
		"app.kubernetes.io/managed-by": "vesta-operator",
		"kubernetes.getvesta.sh/project": app.Spec.Project,
		"kubernetes.getvesta.sh/app":     app.Name,
	}
}

func (r *VestaAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Complete(r)
}
