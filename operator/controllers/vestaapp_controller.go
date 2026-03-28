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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
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

const vestaAppFinalizer = "kubernetes.getvesta.sh/app-cleanup"

func (r *VestaAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var app vestav1alpha1.VestaApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion: clean up resources in target namespaces
	if !app.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&app, vestaAppFinalizer) {
			if err := r.cleanupApp(ctx, &app); err != nil {
				logger.Error(err, "failed to clean up app resources")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&app, vestaAppFinalizer)
			if err := r.Update(ctx, &app); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	needsUpdate := false
	if !controllerutil.ContainsFinalizer(&app, vestaAppFinalizer) {
		controllerutil.AddFinalizer(&app, vestaAppFinalizer)
		needsUpdate = true
	}

	logger.Info("reconciling VestaApp", "name", app.Name, "project", app.Spec.Project)

	if app.Labels == nil {
		app.Labels = map[string]string{}
	}
	if app.Labels["kubernetes.getvesta.sh/project"] != app.Spec.Project || app.Labels["kubernetes.getvesta.sh/app"] != app.Name {
		app.Labels["kubernetes.getvesta.sh/project"] = app.Spec.Project
		app.Labels["kubernetes.getvesta.sh/app"] = app.Name
		needsUpdate = true
	}
	if needsUpdate {
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
		// Return early — the update triggers a re-queue with fresh resourceVersion
		return ctrl.Result{}, nil
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

	if err := r.updateStatusRunning(ctx, req.NamespacedName); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// resolveTargetNamespaces determines which {project}-{env} namespaces to deploy
// into. If spec.environments is set, only those environments are targeted.
// Otherwise all environments for the project are used with default config.
func (r *VestaAppReconciler) resolveTargetNamespaces(ctx context.Context, app *vestav1alpha1.VestaApp) ([]targetEnv, error) {
	if len(app.Spec.Environments) > 0 {
		targets := make([]targetEnv, 0, len(app.Spec.Environments))
		for _, env := range app.Spec.Environments {
			targets = append(targets, targetEnv{
				Namespace: fmt.Sprintf("%s-%s", app.Spec.Project, env.Name),
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
			Namespace: fmt.Sprintf("%s-%s", app.Spec.Project, env.Name),
			Config:    vestav1alpha1.AppEnvironmentConfig{Name: env.Name},
		})
	}
	return targets, nil
}

func (r *VestaAppReconciler) cleanupApp(ctx context.Context, app *vestav1alpha1.VestaApp) error {
	logger := log.FromContext(ctx)
	logger.Info("cleaning up resources for deleted app", "name", app.Name, "project", app.Spec.Project)

	targets, err := r.resolveTargetNamespaces(ctx, app)
	if err != nil {
		return fmt.Errorf("resolve namespaces for cleanup: %w", err)
	}

	for _, target := range targets {
		// Delete Deployment
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: target.Namespace},
		}
		if err := r.Delete(ctx, deploy); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete deployment %s/%s: %w", target.Namespace, app.Name, err)
		}

		// Delete Service
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: target.Namespace},
		}
		if err := r.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete service %s/%s: %w", target.Namespace, app.Name, err)
		}

		// Delete Ingress
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: target.Namespace},
		}
		if err := r.Delete(ctx, ing); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete ingress %s/%s: %w", target.Namespace, app.Name, err)
		}

		// Delete HPA
		hpa := &autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: target.Namespace},
		}
		if err := r.Delete(ctx, hpa); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete hpa %s/%s: %w", target.Namespace, app.Name, err)
		}

		logger.Info("cleaned up resources", "namespace", target.Namespace, "app", app.Name)
	}

	return nil
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

// copyPullSecrets copies referenced registry secrets from vesta-system to the target namespace.
func (r *VestaAppReconciler) copyPullSecrets(ctx context.Context, refs []corev1.LocalObjectReference, targetNS string) error {
	for _, ref := range refs {
		// Get the source secret from vesta-system
		src := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: "vesta-system", Name: ref.Name}, src); err != nil {
			if errors.IsNotFound(err) {
				continue // secret doesn't exist in vesta-system, may already be in target ns
			}
			return fmt.Errorf("get pull secret %s: %w", ref.Name, err)
		}

		// Create or update in target namespace
		dst := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ref.Name,
				Namespace: targetNS,
			},
		}
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dst, func() error {
			dst.Type = src.Type
			dst.Data = src.Data
			dst.Labels = map[string]string{
				"app.kubernetes.io/managed-by": "vesta-operator",
				"kubernetes.getvesta.sh/type":  "registry-copy",
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("copy pull secret %s to %s: %w", ref.Name, targetNS, err)
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

	// Fetch project for imagePullSecrets
	var project vestav1alpha1.VestaProject
	var projectPullSecrets []corev1.LocalObjectReference
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Project}, &project); err == nil {
		projectPullSecrets = project.Spec.ImagePullSecrets
	}

	container := r.buildContainer(app)
	podSpec := r.buildPodSpec(app, container, projectPullSecrets, target.Config.ImagePullSecrets)

	// Copy referenced registry secrets to target namespace
	if err := r.copyPullSecrets(ctx, podSpec.ImagePullSecrets, target.Namespace); err != nil {
		log.FromContext(ctx).Error(err, "failed to copy pull secrets", "namespace", target.Namespace)
	}

	var op controllerutil.OperationResult
	err := retry.OnError(retry.DefaultRetry, isRetriable, func() error {
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: target.Namespace,
			},
		}

		var createErr error
		op, createErr = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
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
		return createErr
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

	// Set default resource requests so HPA can calculate utilization percentages
	if container.Resources.Requests == nil {
		container.Resources.Requests = corev1.ResourceList{}
	}
	if _, ok := container.Resources.Requests[corev1.ResourceCPU]; !ok {
		container.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("100m")
	}
	if _, ok := container.Resources.Requests[corev1.ResourceMemory]; !ok {
		container.Resources.Requests[corev1.ResourceMemory] = resource.MustParse("128Mi")
	}

	return container
}

func (r *VestaAppReconciler) buildPodSpec(app *vestav1alpha1.VestaApp, container corev1.Container, projectPullSecrets, envPullSecrets []corev1.LocalObjectReference) corev1.PodSpec {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
	}

	// Merge imagePullSecrets: project-level, then app-level, then env-level overrides
	seen := map[string]bool{}
	addPullSecret := func(refs []corev1.LocalObjectReference) {
		for _, ref := range refs {
			if !seen[ref.Name] {
				podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{Name: ref.Name})
				seen[ref.Name] = true
			}
		}
	}
	addPullSecret(projectPullSecrets)
	if app.Spec.Image != nil {
		addPullSecret(app.Spec.Image.ImagePullSecrets)
	}
	addPullSecret(envPullSecrets)

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

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
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
	})
}

func (r *VestaAppReconciler) reconcileIngress(ctx context.Context, app *vestav1alpha1.VestaApp, namespace string) error {
	labels := r.labelsForApp(app)
	pathType := networkingv1.PathTypePrefix

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
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
	})
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

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
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
	})
}

func (r *VestaAppReconciler) updateStatusRunning(ctx context.Context, key client.ObjectKey) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var app vestav1alpha1.VestaApp
		if err := r.Get(ctx, key, &app); err != nil {
			return err
		}
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
		return r.Status().Update(ctx, &app)
	})
}

func (r *VestaAppReconciler) updateStatusFailed(ctx context.Context, app *vestav1alpha1.VestaApp, reconcileErr error) (ctrl.Result, error) {
	_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest vestav1alpha1.VestaApp
		if err := r.Get(ctx, client.ObjectKeyFromObject(app), &latest); err != nil {
			return err
		}
		latest.Status.Phase = "Failed"
		return r.Status().Update(ctx, &latest)
	})
	return ctrl.Result{}, reconcileErr
}

// isRetriable returns true for errors that are safe to retry (conflicts and already-exists)
func isRetriable(err error) bool {
	return errors.IsConflict(err) || errors.IsAlreadyExists(err)
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
		Complete(r)
}
