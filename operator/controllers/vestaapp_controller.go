package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
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
	Scheme         *runtime.Scheme
	ConfigResolver *ConfigResolver
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
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=cronjobs;jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create

const vestaAppFinalizer = "kubernetes.getvesta.sh/app-cleanup"

func (r *VestaAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Refresh global config on each reconcile so we pick up VestaConfig changes
	if err := r.ConfigResolver.Refresh(ctx); err != nil {
		logger.Error(err, "failed to refresh VestaConfig")
	}

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

		if err := r.reconcileServiceAccount(ctx, &app, target); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if err := r.reconcilePVCs(ctx, &app, target.Namespace); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if err := r.reconcileDeployment(ctx, &app, target, projectLabels, projectAnnotations); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if err := r.reconcileService(ctx, &app, target); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if app.Spec.Ingress != nil {
			if err := r.reconcileIngress(ctx, &app, target); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
		}

		if target.Config.Autoscale != nil && target.Config.Autoscale.Enabled {
			if err := r.reconcileHPA(ctx, &app, target); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
		}

		if len(app.Spec.Cronjobs) > 0 {
			if err := r.reconcileCronJobs(ctx, &app, target, projectLabels, projectAnnotations); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
		}
	}

	if err := r.updateStatus(ctx, req.NamespacedName); err != nil {
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
		// Delete ServiceAccount
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: target.Namespace},
		}
		if err := r.Delete(ctx, sa); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete serviceaccount %s/%s: %w", target.Namespace, app.Name, err)
		}

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

		// Delete CronJobs
		var cronJobs batchv1.CronJobList
		if err := r.List(ctx, &cronJobs, client.InNamespace(target.Namespace), client.MatchingLabels{
			"kubernetes.getvesta.sh/app": app.Name,
		}); err == nil {
			for i := range cronJobs.Items {
				if err := r.Delete(ctx, &cronJobs.Items[i]); err != nil && !errors.IsNotFound(err) {
					return fmt.Errorf("delete cronjob %s/%s: %w", target.Namespace, cronJobs.Items[i].Name, err)
				}
			}
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

// reconcileServiceAccount creates a ServiceAccount per app per namespace with
// imagePullSecrets attached. Some registries require secrets on the SA rather
// than (or in addition to) the pod spec.
func (r *VestaAppReconciler) reconcileServiceAccount(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv) error {
	// Collect all pull secrets (same merge logic as buildPodSpec)
	var projectPullSecrets []corev1.LocalObjectReference
	var project vestav1alpha1.VestaProject
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Project}, &project); err == nil {
		projectPullSecrets = project.Spec.ImagePullSecrets
	}

	seen := map[string]bool{}
	var merged []corev1.LocalObjectReference
	addRef := func(refs []corev1.LocalObjectReference) {
		for _, ref := range refs {
			if !seen[ref.Name] {
				merged = append(merged, corev1.LocalObjectReference{Name: ref.Name})
				seen[ref.Name] = true
			}
		}
	}
	addRef(projectPullSecrets)
	if app.Spec.Image != nil {
		addRef(app.Spec.Image.ImagePullSecrets)
	}
	addRef(target.Config.ImagePullSecrets)

	// Copy secrets to the target namespace first
	if err := r.copyPullSecrets(ctx, merged, target.Namespace); err != nil {
		log.FromContext(ctx).Error(err, "failed to copy pull secrets for SA", "namespace", target.Namespace)
	}

	labels := r.labelsForApp(app)

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: target.Namespace,
			},
		}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
			sa.Labels = labels
			sa.ImagePullSecrets = merged
			return nil
		})
		if err != nil {
			return fmt.Errorf("reconcile serviceaccount %s/%s: %w", target.Namespace, app.Name, err)
		}

		log.FromContext(ctx).Info("serviceaccount reconciled", "namespace", target.Namespace, "pullSecrets", len(merged))
		return nil
	})
}

func (r *VestaAppReconciler) reconcileDeployment(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv, projectLabels, projectAnnotations map[string]string) error {
	labels := r.labelsForApp(app)
	replicas := int32(1)
	if target.Config.Replicas != nil {
		replicas = *target.Config.Replicas
	}

	// When autoscaling is enabled, don't override replicas — let the HPA control them.
	autoscalingEnabled := target.Config.Autoscale != nil && target.Config.Autoscale.Enabled

	// Scale-to-Zero: if sleep is enabled and the app is marked as sleeping, set replicas to 0
	sleepActive := false
	if app.Spec.Sleep != nil && app.Spec.Sleep.Enabled {
		if app.Status.Phase == "Sleeping" {
			replicas = 0
			sleepActive = true
		}
	}

	// Fetch project for imagePullSecrets
	var project vestav1alpha1.VestaProject
	var projectPullSecrets []corev1.LocalObjectReference
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Project}, &project); err == nil {
		projectPullSecrets = project.Spec.ImagePullSecrets
	}

	container := r.buildContainer(app, target.Config.Resources, target.Config.Name, target.Config.Image)

	// Auto-inject the per-app secret ("{appName}-secrets") as envFrom if it exists in the target namespace.
	// This secret is created by the API when users add per-environment secrets.
	appSecretName := app.Name + "-secrets"
	appSecrets := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: appSecretName}, appSecrets); err == nil {
		// Check it's not already referenced via explicit spec.runtime.secrets
		alreadyBound := false
		for _, sb := range app.Spec.Runtime.Secrets {
			if sb.SecretRef != nil && sb.SecretRef.Name == appSecretName {
				alreadyBound = true
				break
			}
		}
		if !alreadyBound {
			container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: appSecretName},
				},
			})
		}
	}

	podSpec := r.buildPodSpec(app, container, projectPullSecrets, target.Config.ImagePullSecrets)

	// Set the per-app ServiceAccount (which has imagePullSecrets attached)
	podSpec.ServiceAccountName = app.Name

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

			deploy.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: labels,
			}

			// Compute a hash of the pod spec so that any change (e.g. pod size,
			// env vars, image) forces Kubernetes to trigger a rolling update.
			specHash := computePodSpecHash(podSpec)

			deploy.Spec.Template = corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					Annotations: map[string]string{
						"kubernetes.getvesta.sh/spec-hash": specHash,
					},
				},
				Spec: podSpec,
			}

			// Only set replicas when autoscaling is NOT enabled;
			// otherwise let the HPA manage the replica count.
			// When sleep is active, always force replicas to 0.
			if sleepActive {
				deploy.Spec.Replicas = &replicas
			} else if !autoscalingEnabled {
				deploy.Spec.Replicas = &replicas
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

func (r *VestaAppReconciler) buildContainer(app *vestav1alpha1.VestaApp, envResources *vestav1alpha1.ResourceConfig, envName string, envImage *vestav1alpha1.ImageConfig) corev1.Container {
	image := "placeholder:latest"
	// Per-environment image override takes precedence over app-level image
	if envImage != nil && envImage.Repository != "" {
		tag := "latest"
		if envImage.Tag != "" {
			tag = envImage.Tag
		}
		image = fmt.Sprintf("%s:%s", envImage.Repository, tag)
	} else if envImage != nil && envImage.Tag != "" && app.Spec.Image != nil {
		// Environment overrides only the tag, keep the app-level repository
		image = fmt.Sprintf("%s:%s", app.Spec.Image.Repository, envImage.Tag)
	} else if app.Spec.Image != nil {
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

	pullPolicy := corev1.PullPolicy("")
	if envImage != nil && envImage.PullPolicy != "" {
		pullPolicy = envImage.PullPolicy
	} else if app.Spec.Image != nil && app.Spec.Image.PullPolicy != "" {
		pullPolicy = app.Spec.Image.PullPolicy
	}
	if pullPolicy != "" {
		container.ImagePullPolicy = pullPolicy
	}

	if app.Spec.Service != nil && len(app.Spec.Service.Ports) > 0 {
		var containerPorts []corev1.ContainerPort
		for _, p := range app.Spec.Service.Ports {
			protocol := corev1.ProtocolTCP
			if p.Protocol != "" {
				protocol = corev1.Protocol(p.Protocol)
			}
			targetPort := p.TargetPort
			if targetPort == 0 {
				targetPort = p.Port
			}
			containerPorts = append(containerPorts, corev1.ContainerPort{
				Name:          p.Name,
				ContainerPort: targetPort,
				Protocol:      protocol,
			})
		}
		container.Ports = containerPorts
	} else if app.Spec.Runtime.Port > 0 {
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
		// Skip if this binding is scoped to specific environments and the current one isn't in the list
		if len(sb.Environments) > 0 {
			match := false
			for _, e := range sb.Environments {
				if e == envName {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

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

	// Resolve resources: per-env overrides app-level, size name resolves via ConfigResolver
	effectiveResources := app.Spec.Resources
	if envResources != nil {
		effectiveResources = envResources
	}

	if effectiveResources != nil {
		if effectiveResources.Size != "" && r.ConfigResolver != nil {
			reqs, lims := r.ConfigResolver.ResolvePodSize(effectiveResources.Size)
			container.Resources.Requests = reqs
			container.Resources.Limits = lims
		} else {
			if effectiveResources.Requests != nil {
				container.Resources.Requests = effectiveResources.Requests
			}
			if effectiveResources.Limits != nil {
				container.Resources.Limits = effectiveResources.Limits
			}
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

	// Health checks (liveness + readiness probes)
	if hc := app.Spec.HealthCheck; hc != nil {
		if probe := r.buildProbe(hc, app.Spec.Runtime.Port); probe != nil {
			container.LivenessProbe = probe.DeepCopy()
			container.ReadinessProbe = probe.DeepCopy()
		}
	}

	return container
}

func (r *VestaAppReconciler) buildProbe(hc *vestav1alpha1.HealthCheckConfig, runtimePort int32) *corev1.Probe {
	probe := &corev1.Probe{}

	switch hc.Type {
	case "http":
		port := hc.Port
		if port == 0 {
			port = runtimePort
		}
		if port == 0 {
			return nil
		}
		path := hc.Path
		if path == "" {
			path = "/"
		}
		probe.ProbeHandler = corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt32(port),
			},
		}
	case "tcp":
		port := hc.Port
		if port == 0 {
			port = runtimePort
		}
		if port == 0 {
			return nil
		}
		probe.ProbeHandler = corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt32(port),
			},
		}
	case "exec":
		if hc.Command != "" {
			probe.ProbeHandler = corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/sh", "-c", hc.Command},
				},
			}
		}
	}

	if hc.InitialDelaySeconds > 0 {
		probe.InitialDelaySeconds = hc.InitialDelaySeconds
	}
	if hc.PeriodSeconds > 0 {
		probe.PeriodSeconds = hc.PeriodSeconds
	} else {
		probe.PeriodSeconds = 10
	}
	if hc.TimeoutSeconds > 0 {
		probe.TimeoutSeconds = hc.TimeoutSeconds
	}
	if hc.FailureThreshold > 0 {
		probe.FailureThreshold = hc.FailureThreshold
	}
	if hc.SuccessThreshold > 0 {
		probe.SuccessThreshold = hc.SuccessThreshold
	}

	return probe
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

func (r *VestaAppReconciler) reconcilePVCs(ctx context.Context, app *vestav1alpha1.VestaApp, namespace string) error {
	logger := log.FromContext(ctx)
	for _, v := range app.Spec.Runtime.Volumes {
		if v.PersistentVolumeClaim == nil {
			continue
		}
		size := v.PersistentVolumeClaim.Size
		if size == "" {
			size = "1Gi"
		}
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      v.PersistentVolumeClaim.ClaimName,
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by":   "vesta",
					"kubernetes.getvesta.sh/app":     app.Name,
					"kubernetes.getvesta.sh/project": app.Spec.Project,
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(size),
					},
				},
			},
		}
		existing := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, client.ObjectKeyFromObject(pvc), existing)
		if errors.IsNotFound(err) {
			logger.Info("creating PVC", "name", pvc.Name, "namespace", namespace, "size", size)
			if err := r.Create(ctx, pvc); err != nil {
				return fmt.Errorf("create PVC %s: %w", pvc.Name, err)
			}
		} else if err != nil {
			return fmt.Errorf("get PVC %s: %w", pvc.Name, err)
		}
	}
	return nil
}

func (r *VestaAppReconciler) reconcileService(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv) error {
	// Determine service config: per-environment override → app-level → legacy runtime.port
	svcCfg := app.Spec.Service
	if target.Config.Service != nil {
		// Merge: env overrides app-level service config
		merged := &vestav1alpha1.ServiceConfig{}
		if svcCfg != nil {
			merged.Type = svcCfg.Type
			merged.Ports = svcCfg.Ports
		}
		if target.Config.Service.Type != "" {
			merged.Type = target.Config.Service.Type
		}
		if len(target.Config.Service.Ports) > 0 {
			merged.Ports = target.Config.Service.Ports
		}
		svcCfg = merged
	}

	// Determine service ports: prefer spec.service.ports, fall back to runtime.port
	var svcPorts []corev1.ServicePort
	var svcType corev1.ServiceType

	if svcCfg != nil && len(svcCfg.Ports) > 0 {
		for _, p := range svcCfg.Ports {
			protocol := corev1.ProtocolTCP
			if p.Protocol != "" {
				protocol = corev1.Protocol(p.Protocol)
			}
			targetPort := p.TargetPort
			if targetPort == 0 {
				targetPort = p.Port
			}
			sp := corev1.ServicePort{
				Name:       p.Name,
				Port:       p.Port,
				TargetPort: intstr.FromInt32(targetPort),
				Protocol:   protocol,
			}
			if p.NodePort > 0 {
				sp.NodePort = p.NodePort
			}
			svcPorts = append(svcPorts, sp)
		}
		switch svcCfg.Type {
		case "NodePort":
			svcType = corev1.ServiceTypeNodePort
		case "LoadBalancer":
			svcType = corev1.ServiceTypeLoadBalancer
		default:
			svcType = corev1.ServiceTypeClusterIP
		}
	} else if app.Spec.Runtime.Port > 0 {
		// Legacy single-port mode
		svcPorts = []corev1.ServicePort{
			{
				Name:       "http",
				Port:       80,
				TargetPort: intstr.FromInt32(app.Spec.Runtime.Port),
				Protocol:   corev1.ProtocolTCP,
			},
		}
		svcType = corev1.ServiceTypeClusterIP
	} else {
		return nil
	}

	labels := r.labelsForApp(app)

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: target.Namespace,
			},
		}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
			svc.Labels = labels
			svc.Spec.Selector = labels
			svc.Spec.Ports = svcPorts
			svc.Spec.Type = svcType
			return nil
		})
		return err
	})
}

func (r *VestaAppReconciler) reconcileIngress(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv) error {
	labels := r.labelsForApp(app)
	pathType := networkingv1.PathTypePrefix

	// Determine the service port for the ingress backend
	ingressPort := int32(80)
	if app.Spec.Service != nil && len(app.Spec.Service.Ports) > 0 {
		// Prefer port named "http", otherwise use the first port
		ingressPort = app.Spec.Service.Ports[0].Port
		for _, p := range app.Spec.Service.Ports {
			if p.Name == "http" {
				ingressPort = p.Port
				break
			}
		}
	}

	// Resolve per-environment domain: env override → domain template → app-level domain
	domain := app.Spec.Ingress.Domain
	if target.Config.Ingress != nil && target.Config.Ingress.Domain != "" {
		domain = target.Config.Ingress.Domain
	} else if tpl := r.ConfigResolver.GetDomainTemplate(); tpl != "" && target.Config.Name != "" {
		expanded := strings.ReplaceAll(tpl, "{{app}}", app.Name)
		expanded = strings.ReplaceAll(expanded, "{{env}}", target.Config.Name)
		expanded = strings.ReplaceAll(expanded, "{{domain}}", r.ConfigResolver.GetDomain())
		domain = expanded
	}

	// Resolve per-environment TLS: env override → app-level TLS
	tlsEnabled := app.Spec.Ingress.TLS
	if target.Config.Ingress != nil && target.Config.Ingress.TLS != nil {
		tlsEnabled = *target.Config.Ingress.TLS
	}

	// TLS secret name includes env to avoid cross-environment collisions
	tlsSecretName := fmt.Sprintf("%s-tls", app.Name)
	if target.Config.Name != "" {
		tlsSecretName = fmt.Sprintf("%s-%s-tls", app.Name, target.Config.Name)
	}

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: target.Namespace,
			},
		}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
			ing.Labels = labels
			ing.Annotations = map[string]string{}

			clusterIssuer := app.Spec.Ingress.ClusterIssuer
			if clusterIssuer == "" {
				clusterIssuer = r.ConfigResolver.GetClusterIssuer()
			}
			if clusterIssuer != "" {
				ing.Annotations["cert-manager.io/cluster-issuer"] = clusterIssuer
			}

			ingressClassName := app.Spec.Ingress.IngressClassName
			if ingressClassName == "" {
				ingressClassName = r.ConfigResolver.GetIngressClassName()
			}
			if ingressClassName != "" {
				ing.Annotations["kubernetes.io/ingress.class"] = ingressClassName
			}

			for k, v := range app.Spec.Ingress.Annotations {
				ing.Annotations[k] = v
			}

			ing.Spec = networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: domain,
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
													Number: ingressPort,
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

			if ingressClassName != "" {
				ing.Spec.IngressClassName = &ingressClassName
			}

			if tlsEnabled {
				ing.Annotations["traefik.ingress.kubernetes.io/router.tls"] = "true"
				ing.Spec.TLS = []networkingv1.IngressTLS{
					{
						Hosts:      []string{domain},
						SecretName: tlsSecretName,
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
			} else {
				// Default: 5-minute stabilization window for scale-down to avoid flapping
				stabilizationSec := int32(300)
				hpa.Spec.Behavior = &autoscalingv2.HorizontalPodAutoscalerBehavior{
					ScaleDown: &autoscalingv2.HPAScalingRules{
						StabilizationWindowSeconds: &stabilizationSec,
					},
				}
			}

			return nil
		})
		return err
	})
}

func (r *VestaAppReconciler) reconcileCronJobs(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv, projectLabels, projectAnnotations map[string]string) error {
	logger := log.FromContext(ctx)
	labels := r.labelsForApp(app)

	// Build the set of desired cronjob names so we can clean up orphans
	desiredCronJobs := map[string]bool{}
	for _, cj := range app.Spec.Cronjobs {
		if r.isCronjobDisabledForEnv(cj, target.Config.Name) {
			continue
		}
		desiredCronJobs[fmt.Sprintf("%s-%s", app.Name, cj.Name)] = true
	}

	// Fetch project for imagePullSecrets
	var project vestav1alpha1.VestaProject
	var projectPullSecrets []corev1.LocalObjectReference
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Project}, &project); err == nil {
		projectPullSecrets = project.Spec.ImagePullSecrets
	}

	for _, cj := range app.Spec.Cronjobs {
		cronjobName := fmt.Sprintf("%s-%s", app.Name, cj.Name)

		// Check per-environment override: skip if disabled
		if r.isCronjobDisabledForEnv(cj, target.Config.Name) {
			logger.Info("cronjob disabled for environment, skipping", "cronjob", cj.Name, "environment", target.Config.Name)
			continue
		}

		// Resolve effective schedule (per-environment override wins)
		effectiveSchedule := r.resolveCronjobSchedule(cj, target.Config.Name)

		// Build the container: same image, env, secrets, volumes as the main app — only override command
		container := r.buildContainer(app, cj.Resources, target.Config.Name, target.Config.Image)
		container.Name = "job"
		container.Command = []string{"/bin/sh", "-c", cj.Command}
		container.Args = nil
		container.Ports = nil
		container.LivenessProbe = nil
		container.ReadinessProbe = nil

		// Auto-inject per-app secret if it exists in the target namespace
		appSecretName := app.Name + "-secrets"
		appSecrets := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: appSecretName}, appSecrets); err == nil {
			alreadyBound := false
			for _, sb := range app.Spec.Runtime.Secrets {
				if sb.SecretRef != nil && sb.SecretRef.Name == appSecretName {
					alreadyBound = true
					break
				}
			}
			if !alreadyBound {
				container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
					SecretRef: &corev1.SecretEnvSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: appSecretName},
					},
				})
			}
		}

		podSpec := r.buildPodSpec(app, container, projectPullSecrets, target.Config.ImagePullSecrets)
		podSpec.ServiceAccountName = app.Name
		podSpec.RestartPolicy = corev1.RestartPolicyOnFailure

		cronjobLabels := make(map[string]string)
		for k, v := range labels {
			cronjobLabels[k] = v
		}
		cronjobLabels["kubernetes.getvesta.sh/cronjob"] = cj.Name
		for k, v := range projectLabels {
			cronjobLabels[k] = v
		}
		if app.Spec.CustomConfig != nil {
			for k, v := range app.Spec.CustomConfig.Labels {
				cronjobLabels[k] = v
			}
		}

		cronjobAnnotations := make(map[string]string)
		for k, v := range projectAnnotations {
			cronjobAnnotations[k] = v
		}
		if app.Spec.CustomConfig != nil {
			for k, v := range app.Spec.CustomConfig.Annotations {
				cronjobAnnotations[k] = v
			}
		}

		successLimit := int32(3)
		failedLimit := int32(1)

		err := retry.OnError(retry.DefaultRetry, isRetriable, func() error {
			cronJob := &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cronjobName,
					Namespace: target.Namespace,
				},
			}

			_, createErr := controllerutil.CreateOrUpdate(ctx, r.Client, cronJob, func() error {
				cronJob.Labels = cronjobLabels
				cronJob.Annotations = cronjobAnnotations
				cronJob.Spec = batchv1.CronJobSpec{
					Schedule:                   effectiveSchedule,
					ConcurrencyPolicy:          batchv1.ForbidConcurrent,
					SuccessfulJobsHistoryLimit: &successLimit,
					FailedJobsHistoryLimit:     &failedLimit,
					JobTemplate: batchv1.JobTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: cronjobLabels,
						},
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Labels: cronjobLabels,
								},
								Spec: podSpec,
							},
						},
					},
				}
				return nil
			})
			return createErr
		})
		if err != nil {
			return fmt.Errorf("reconcile cronjob %s in %s: %w", cronjobName, target.Namespace, err)
		}

		logger.Info("cronjob reconciled", "namespace", target.Namespace, "cronjob", cronjobName)
	}

	// Clean up orphaned CronJobs: CronJobs that belong to this app but are no longer in spec
	var existingCronJobs batchv1.CronJobList
	if err := r.List(ctx, &existingCronJobs, client.InNamespace(target.Namespace), client.MatchingLabels{
		"kubernetes.getvesta.sh/app": app.Name,
	}); err != nil {
		return fmt.Errorf("list cronjobs for cleanup in %s: %w", target.Namespace, err)
	}

	for i := range existingCronJobs.Items {
		existing := &existingCronJobs.Items[i]
		if !desiredCronJobs[existing.Name] {
			if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("delete orphaned cronjob %s/%s: %w", target.Namespace, existing.Name, err)
			}
			logger.Info("deleted orphaned cronjob", "namespace", target.Namespace, "cronjob", existing.Name)
		}
	}

	return nil
}

// isCronjobDisabledForEnv checks if a cronjob has been explicitly disabled for a given environment.
func (r *VestaAppReconciler) isCronjobDisabledForEnv(cj vestav1alpha1.CronjobConfig, envName string) bool {
	for _, envOverride := range cj.Environments {
		if envOverride.Name == envName && envOverride.Enabled != nil && !*envOverride.Enabled {
			return true
		}
	}
	return false
}

// resolveCronjobSchedule returns the effective schedule for a cronjob in a given environment.
// If a per-environment schedule override exists, it takes precedence over the default.
func (r *VestaAppReconciler) resolveCronjobSchedule(cj vestav1alpha1.CronjobConfig, envName string) string {
	for _, envOverride := range cj.Environments {
		if envOverride.Name == envName && envOverride.Schedule != "" {
			return envOverride.Schedule
		}
	}
	return cj.Schedule
}

func (r *VestaAppReconciler) updateStatus(ctx context.Context, key client.ObjectKey) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var app vestav1alpha1.VestaApp
		if err := r.Get(ctx, key, &app); err != nil {
			return err
		}

		now := time.Now().UTC().Format(time.RFC3339)

		// --- Compute phase from actual Deployment + Pod health ---
		targetNamespaces, err := r.resolveTargetNamespaces(ctx, &app)
		if err != nil {
			// Can't resolve — keep current phase, just update metadata
			app.Status.LastDeployedAt = now
			return r.Status().Update(ctx, &app)
		}

		var totalDesired, totalAvailable, totalReady int32
		hasCrashLoop := false
		hasImagePullErr := false
		autoscaleActive := false

		for _, target := range targetNamespaces {
			// Check if any environment has autoscale
			if target.Config.Autoscale != nil && target.Config.Autoscale.Enabled {
				autoscaleActive = true
			}

			// Get the Deployment for this environment
			var deploy appsv1.Deployment
			deployKey := client.ObjectKey{Namespace: target.Namespace, Name: app.Name}
			if err := r.Get(ctx, deployKey, &deploy); err != nil {
				continue // Deployment may not exist yet
			}

			if deploy.Spec.Replicas != nil {
				totalDesired += *deploy.Spec.Replicas
			}
			totalAvailable += deploy.Status.AvailableReplicas
			totalReady += deploy.Status.ReadyReplicas

			// Check pods for CrashLoopBackOff / ImagePullBackOff
			var podList corev1.PodList
			if err := r.List(ctx, &podList,
				client.InNamespace(target.Namespace),
				client.MatchingLabels{"kubernetes.getvesta.sh/app": app.Name},
			); err != nil {
				continue
			}

			for _, pod := range podList.Items {
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.State.Waiting != nil {
						switch cs.State.Waiting.Reason {
						case "CrashLoopBackOff":
							hasCrashLoop = true
						case "ImagePullBackOff", "ErrImagePull":
							hasImagePullErr = true
						}
					}
				}
				for _, cs := range pod.Status.InitContainerStatuses {
					if cs.State.Waiting != nil {
						switch cs.State.Waiting.Reason {
						case "CrashLoopBackOff":
							hasCrashLoop = true
						case "ImagePullBackOff", "ErrImagePull":
							hasImagePullErr = true
						}
					}
				}
			}
		}

		// Determine phase
		sleepEnabled := app.Spec.Sleep != nil && app.Spec.Sleep.Enabled
		switch {
		case sleepEnabled && totalDesired == 0:
			app.Status.Phase = "Sleeping"
		case hasImagePullErr:
			app.Status.Phase = "Failed"
		case hasCrashLoop:
			app.Status.Phase = "CrashLoopBackOff"
		case totalDesired > 0 && totalAvailable == 0:
			app.Status.Phase = "Deploying"
		case totalDesired > 0 && totalAvailable < totalDesired:
			app.Status.Phase = "Degraded"
		case totalDesired > 0 && totalAvailable >= totalDesired:
			app.Status.Phase = "Running"
		default:
			app.Status.Phase = "Pending"
		}

		// Populate scaling status
		app.Status.Scaling = &vestav1alpha1.ScalingStatus{
			CurrentReplicas:  totalReady,
			DesiredReplicas:  totalDesired,
			AutoscalerActive: autoscaleActive,
		}

		// --- Existing metadata updates ---
		if app.Spec.Image != nil {
			newImage := fmt.Sprintf("%s:%s", app.Spec.Image.Repository, app.Spec.Image.Tag)
			if newImage != app.Status.CurrentImage {
				nextVersion := 1
				if len(app.Status.DeploymentHistory) > 0 {
					nextVersion = app.Status.DeploymentHistory[len(app.Status.DeploymentHistory)-1].Version + 1
				}
				deployEnv := ""
				if ann := app.GetAnnotations(); ann != nil {
					deployEnv = ann["vesta.sh/last-deploy-environment"]
				}
				app.Status.DeploymentHistory = append(app.Status.DeploymentHistory, vestav1alpha1.DeploymentRecord{
					Version:     nextVersion,
					Image:       newImage,
					Environment: deployEnv,
					DeployedAt:  now,
				})
			}
			app.Status.CurrentImage = newImage
		}
		if app.Spec.Ingress != nil {
			scheme := "http"
			if app.Spec.Ingress.TLS {
				scheme = "https"
			}
			app.Status.URL = fmt.Sprintf("%s://%s", scheme, app.Spec.Ingress.Domain)
		}
		app.Status.LastDeployedAt = now
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

// computePodSpecHash returns a short SHA-256 hex digest of the serialised PodSpec.
// This is used as a PodTemplate annotation so that any spec change (resources,
// env vars, image, volumes, etc.) forces a Deployment rollout.
func computePodSpecHash(spec corev1.PodSpec) string {
	data, _ := json.Marshal(spec)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}
