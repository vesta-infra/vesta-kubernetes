package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingressclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
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

		if app.Spec.Ingress != nil || target.Config.Ingress != nil {
			if err := r.reconcileIngress(ctx, &app, target); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
			// Reconcile HTTPS redirect middleware for Traefik when TLS is enabled
			r.reconcileHTTPSRedirectMiddleware(ctx, &app, target)
		} else {
			// Clean up orphaned Ingress if ingress config was removed
			orphanIng := &networkingv1.Ingress{}
			if err := r.Client.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: app.Name}, orphanIng); err == nil {
				if err := r.Client.Delete(ctx, orphanIng); err != nil {
					logger.Error(err, "failed to delete orphaned ingress", "namespace", target.Namespace)
				}
			}
		}

		// Reconcile redirect ingress (handles both creation and cleanup)
		if err := r.reconcileRedirectIngress(ctx, &app, target); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
		}

		if target.Config.Autoscale != nil && target.Config.Autoscale.Enabled {
			if err := r.reconcileHPA(ctx, &app, target); err != nil {
				return r.updateStatusFailed(ctx, &app, err)
			}
		}

		// Always run: with no cronjobs in the spec this prunes any left behind
		// by a previous revision.
		if err := r.reconcileCronJobs(ctx, &app, target, projectLabels, projectAnnotations); err != nil {
			return r.updateStatusFailed(ctx, &app, err)
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

	// Stopped: if the app is explicitly stopped, set replicas to 0
	if app.Status.Phase == "Stopped" {
		replicas = 0
		sleepActive = true // reuse the sleepActive flag to force replicas
	}

	// Fetch project for imagePullSecrets
	var project vestav1alpha1.VestaProject
	var projectPullSecrets []corev1.LocalObjectReference
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Project}, &project); err == nil {
		projectPullSecrets = project.Spec.ImagePullSecrets
	}

	container := r.buildContainer(app, target.Config.Resources, target.Config.Name, target.Config.Image)

	// configFingerprint accumulates ResourceVersions of secrets/configmaps that
	// feed env vars into the pod via envFrom. Including these in the rollout
	// hash forces a new ReplicaSet whenever any referenced data changes.
	var configFingerprintParts []string

	// Auto-inject the per-app secret ("{appName}-secrets") as envFrom if it exists in the target namespace.
	// This secret is created by the API when users add per-environment secrets.
	appSecretName := app.Name + "-secrets"
	appSecrets := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: appSecretName}, appSecrets); err == nil {
		configFingerprintParts = append(configFingerprintParts, fmt.Sprintf("secret/%s=%s", appSecretName, appSecrets.ResourceVersion))
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

	// Track ResourceVersions of any explicitly-bound secrets so updates to those
	// also trigger rollouts.
	for _, sb := range app.Spec.Runtime.Secrets {
		if sb.SecretRef == nil || sb.SecretRef.Name == "" {
			continue
		}
		boundSecret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: sb.SecretRef.Name}, boundSecret); err == nil {
			configFingerprintParts = append(configFingerprintParts, fmt.Sprintf("secret/%s=%s", sb.SecretRef.Name, boundSecret.ResourceVersion))
		}
	}

	// Auto-inject the per-app ConfigMap ("{appName}-envvars") as envFrom if it exists.
	// This ConfigMap is created by the API when users add per-environment env vars.
	appEnvVarsCM := app.Name + "-envvars"
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: appEnvVarsCM}, cm); err == nil {
		configFingerprintParts = append(configFingerprintParts, fmt.Sprintf("configmap/%s=%s", appEnvVarsCM, cm.ResourceVersion))
		container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: appEnvVarsCM},
			},
		})
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

			// Compute a hash of the pod spec + the data-fingerprint of envFrom
			// sources (Secret/ConfigMap ResourceVersions) so that any change
			// — pod size, env vars, image, volumes, OR the contents of a
			// referenced Secret/ConfigMap — forces a rolling update.
			specHash := computeRolloutHash(podSpec, configFingerprintParts)

			// Preserve a manually-applied restart annotation (set by the API's
			// RestartApp / restartDeployment helpers). Without this, the next
			// reconcile would overwrite the pod template annotations and cancel
			// the rollout the user just requested.
			tplAnnotations := map[string]string{
				"kubernetes.getvesta.sh/spec-hash": specHash,
			}
			if existing := deploy.Spec.Template.Annotations["kubernetes.getvesta.sh/restartedAt"]; existing != "" {
				tplAnnotations["kubernetes.getvesta.sh/restartedAt"] = existing
			}

			deploy.Spec.Template = corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: tplAnnotations,
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

// resolveImage returns the image an environment runs, applying the per-environment
// override on top of the app-level image. This is the single source of truth for
// "what is deployed where" — status reporting must use it rather than reading
// spec.image directly, which is only the default for environments without a tag.
func resolveImage(app *vestav1alpha1.VestaApp, envImage *vestav1alpha1.ImageConfig) string {
	// Per-environment image override takes precedence over app-level image
	if envImage != nil && envImage.Repository != "" {
		tag := "latest"
		if envImage.Tag != "" {
			tag = envImage.Tag
		}
		return fmt.Sprintf("%s:%s", envImage.Repository, tag)
	}
	if envImage != nil && envImage.Tag != "" && app.Spec.Image != nil {
		// Environment overrides only the tag, keep the app-level repository
		return fmt.Sprintf("%s:%s", app.Spec.Image.Repository, envImage.Tag)
	}
	if app.Spec.Image != nil {
		tag := "latest"
		if app.Spec.Image.Tag != "" {
			tag = app.Spec.Image.Tag
		}
		return fmt.Sprintf("%s:%s", app.Spec.Image.Repository, tag)
	}
	return "placeholder:latest"
}

// Kubernetes requires port names to be unique within a container and within a
// Service, at most 15 characters, lowercase alphanumeric or '-', and to contain
// at least one letter. A spec that violates this is rejected on every reconcile,
// so the app can never converge -- the helpers below repair what they can instead
// of letting one bad field wedge the whole object. The API rejects these specs at
// the door; this is for specs already stored from before that check existed.

var portNameInvalidChars = regexp.MustCompile(`[^a-z0-9-]`)

// sanitizePortName coerces a name into a legal port name, falling back to one
// derived from the port number when nothing usable is left.
func sanitizePortName(name string, port int32) string {
	n := portNameInvalidChars.ReplaceAllString(strings.ToLower(name), "-")
	n = strings.Trim(n, "-")
	if len(n) > 15 {
		n = strings.Trim(n[:15], "-")
	}
	// Must contain at least one letter, so a purely numeric name is not legal.
	if n == "" || !strings.ContainsAny(n, "abcdefghijklmnopqrstuvwxyz") {
		n = fmt.Sprintf("p-%d", port)
		if len(n) > 15 {
			n = n[:15]
		}
	}
	return n
}

// uniquePortName returns a legal name not already in used, suffixing -2, -3, ...
// until it finds one. The returned name is recorded in used.
func uniquePortName(name string, port int32, used map[string]bool) string {
	base := sanitizePortName(name, port)
	candidate := base
	for i := 2; used[candidate]; i++ {
		suffix := fmt.Sprintf("-%d", i)
		trimmed := base
		if len(trimmed)+len(suffix) > 15 {
			trimmed = strings.Trim(base[:15-len(suffix)], "-")
			if trimmed == "" {
				trimmed = "p"
			}
		}
		candidate = trimmed + suffix
	}
	used[candidate] = true
	return candidate
}

// buildContainerPorts maps service ports onto container ports, collapsing entries
// that resolve to the same container port and protocol (listing one twice adds
// nothing) and forcing names to be unique. Container port names are informational
// here -- probes and Service targets both address ports numerically -- so renaming
// a duplicate changes no behaviour.
func buildContainerPorts(ports []vestav1alpha1.ServicePort) []corev1.ContainerPort {
	var out []corev1.ContainerPort
	seen := map[string]bool{}
	usedNames := map[string]bool{}

	for _, p := range ports {
		protocol := corev1.ProtocolTCP
		if p.Protocol != "" {
			protocol = corev1.Protocol(p.Protocol)
		}
		targetPort := p.TargetPort
		if targetPort == 0 {
			targetPort = p.Port
		}
		if targetPort <= 0 {
			continue
		}

		key := fmt.Sprintf("%d/%s", targetPort, protocol)
		if seen[key] {
			continue
		}
		seen[key] = true

		out = append(out, corev1.ContainerPort{
			Name:          uniquePortName(p.Name, targetPort, usedNames),
			ContainerPort: targetPort,
			Protocol:      protocol,
		})
	}
	return out
}

// buildServicePorts maps spec ports onto Service ports, dropping entries that
// repeat a port/protocol pair (a Service cannot expose the same port twice) and
// forcing names to be unique.
func buildServicePorts(ports []vestav1alpha1.ServicePort) []corev1.ServicePort {
	var out []corev1.ServicePort
	seen := map[string]bool{}
	usedNames := map[string]bool{}

	for _, p := range ports {
		if p.Port <= 0 {
			continue
		}
		protocol := corev1.ProtocolTCP
		if p.Protocol != "" {
			protocol = corev1.Protocol(p.Protocol)
		}

		key := fmt.Sprintf("%d/%s", p.Port, protocol)
		if seen[key] {
			continue
		}
		seen[key] = true

		targetPort := p.TargetPort
		if targetPort == 0 {
			targetPort = p.Port
		}

		sp := corev1.ServicePort{
			Name:       uniquePortName(p.Name, p.Port, usedNames),
			Port:       p.Port,
			TargetPort: intstr.FromInt32(targetPort),
			Protocol:   protocol,
		}
		if p.NodePort > 0 {
			sp.NodePort = p.NodePort
		}
		out = append(out, sp)
	}
	return out
}

func (r *VestaAppReconciler) buildContainer(app *vestav1alpha1.VestaApp, envResources *vestav1alpha1.ResourceConfig, envName string, envImage *vestav1alpha1.ImageConfig) corev1.Container {
	image := resolveImage(app, envImage)

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
		container.Ports = buildContainerPorts(app.Spec.Service.Ports)
	} else if app.Spec.Runtime.Port > 0 {
		container.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: app.Spec.Runtime.Port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}

	// Start command handling.
	//   - command + args  -> exec form: command becomes the container entrypoint
	//     (shell-split so multi-word commands like "npm start" work) and args are
	//     passed straight through, exactly like a Kubernetes command/args pair.
	//     This works on distroless/scratch images and preserves PID-1 signalling.
	//   - command only     -> convenience shell form: run as a "/bin/sh -c" one-liner.
	//   - args only        -> override the image CMD while keeping its ENTRYPOINT.
	if app.Spec.Runtime.Command != "" {
		if len(app.Spec.Runtime.Args) > 0 {
			container.Command = shellSplit(app.Spec.Runtime.Command)
			container.Args = app.Spec.Runtime.Args
		} else {
			container.Command = []string{"/bin/sh", "-c", app.Spec.Runtime.Command}
		}
	} else if len(app.Spec.Runtime.Args) > 0 {
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
		svcPorts = buildServicePorts(svcCfg.Ports)
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

// defaultIngressClassAnnotation marks the cluster-wide default IngressClass.
const defaultIngressClassAnnotation = "ingressclass.kubernetes.io/is-default-class"

// resolveIngressClassName resolves which IngressClass an app's ingresses belong to:
// app-level override → platform default (VestaConfig) → the cluster's default IngressClass.
// The last fallback matters because controllers such as Traefik only load an ingress'
// TLS secret for ingresses they own — an unclassed ingress is served with the
// controller's default self-signed certificate instead. Returns "" if none is found.
func (r *VestaAppReconciler) resolveIngressClassName(ctx context.Context, app *vestav1alpha1.VestaApp) string {
	if app.Spec.Ingress != nil && app.Spec.Ingress.IngressClassName != "" {
		return app.Spec.Ingress.IngressClassName
	}
	if name := r.ConfigResolver.GetIngressClassName(); name != "" {
		return name
	}

	classes := &networkingv1.IngressClassList{}
	if err := r.Client.List(ctx, classes); err != nil {
		log.FromContext(ctx).V(1).Info("unable to list IngressClasses to resolve the default", "error", err.Error())
		return ""
	}
	for i := range classes.Items {
		if classes.Items[i].Annotations[defaultIngressClassAnnotation] == "true" {
			return classes.Items[i].Name
		}
	}
	return ""
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

	// Resolve per-environment domains: env override → domain template → app-level domain
	var domains []string
	if target.Config.Ingress != nil && len(target.Config.Ingress.Domains) > 0 {
		domains = target.Config.Ingress.Domains
	} else if target.Config.Ingress != nil && target.Config.Ingress.Domain != "" {
		domains = []string{target.Config.Ingress.Domain}
	} else if tpl := r.ConfigResolver.GetDomainTemplate(); tpl != "" && target.Config.Name != "" {
		expanded := strings.ReplaceAll(tpl, "{{app}}", app.Name)
		expanded = strings.ReplaceAll(expanded, "{{env}}", target.Config.Name)
		expanded = strings.ReplaceAll(expanded, "{{domain}}", r.ConfigResolver.GetDomain())
		domains = []string{expanded}
	} else if app.Spec.Ingress != nil && app.Spec.Ingress.Domain != "" {
		domains = []string{app.Spec.Ingress.Domain}
	} else {
		// No domains resolvable — skip ingress creation
		return nil
	}

	// Filter out empty-string domains
	filtered := domains[:0]
	for _, d := range domains {
		if strings.TrimSpace(d) != "" {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	domains = filtered

	// Resolve per-environment TLS: env override → app-level TLS → default false
	tlsEnabled := false
	if app.Spec.Ingress != nil {
		tlsEnabled = app.Spec.Ingress.TLS
	}
	if target.Config.Ingress != nil && target.Config.Ingress.TLS != nil {
		tlsEnabled = *target.Config.Ingress.TLS
	}

	// TLS secret name includes env to avoid cross-environment collisions
	tlsSecretName := fmt.Sprintf("%s-tls", app.Name)
	if target.Config.Name != "" {
		tlsSecretName = fmt.Sprintf("%s-%s-tls", app.Name, target.Config.Name)
	}

	ingressClassName := r.resolveIngressClassName(ctx, app)

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

			var clusterIssuer string
			if app.Spec.Ingress != nil {
				clusterIssuer = app.Spec.Ingress.ClusterIssuer
			}
			if clusterIssuer == "" {
				clusterIssuer = r.ConfigResolver.GetClusterIssuer()
			}
			if clusterIssuer != "" {
				ing.Annotations["cert-manager.io/cluster-issuer"] = clusterIssuer
			}

			if ingressClassName != "" {
				ing.Annotations["kubernetes.io/ingress.class"] = ingressClassName
			}

			if app.Spec.Ingress != nil {
				for k, v := range app.Spec.Ingress.Annotations {
					ing.Annotations[k] = v
				}
			}
			// Per-environment annotations override app-level
			if target.Config.Ingress != nil {
				for k, v := range target.Config.Ingress.Annotations {
					ing.Annotations[k] = v
				}
			}

			rules := make([]networkingv1.IngressRule, 0, len(domains))
			for _, d := range domains {
				rules = append(rules, networkingv1.IngressRule{
					Host: d,
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
				})
			}

			// Keep any class already on the object (e.g. defaulted by the API server
			// at creation) — the wholesale Spec assignment below would drop it.
			existingClass := ing.Spec.IngressClassName

			ing.Spec = networkingv1.IngressSpec{
				Rules: rules,
			}

			switch {
			case ingressClassName != "":
				ing.Spec.IngressClassName = &ingressClassName
			case existingClass != nil:
				ing.Spec.IngressClassName = existingClass
			}

			if tlsEnabled {
				if strings.Contains(strings.ToLower(ingressClassName), "traefik") {
					ing.Annotations["traefik.ingress.kubernetes.io/router.tls"] = "true"
					// Reference the HTTPS redirect middleware (redirectScheme is no-op for HTTPS requests)
					httpsMiddlewareName := fmt.Sprintf("%s-https-redirect", app.Name)
					middlewareRef := fmt.Sprintf("%s-%s@kubernetescrd", target.Namespace, httpsMiddlewareName)
					// Append to existing middlewares if any (e.g., from per-env annotations)
					if existing, ok := ing.Annotations["traefik.ingress.kubernetes.io/router.middlewares"]; ok && existing != "" {
						ing.Annotations["traefik.ingress.kubernetes.io/router.middlewares"] = existing + "," + middlewareRef
					} else {
						ing.Annotations["traefik.ingress.kubernetes.io/router.middlewares"] = middlewareRef
					}
				}
				ing.Spec.TLS = []networkingv1.IngressTLS{
					{
						Hosts:      domains,
						SecretName: tlsSecretName,
					},
				}
			}

			return nil
		})
		return err
	})
}

func (r *VestaAppReconciler) reconcileRedirectIngress(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv) error {
	logger := log.FromContext(ctx)

	// Resolve redirect domains: per-env override → app-level
	var redirectDomains []string
	if target.Config.Ingress != nil && len(target.Config.Ingress.RedirectDomains) > 0 {
		redirectDomains = target.Config.Ingress.RedirectDomains
	} else if app.Spec.Ingress != nil && len(app.Spec.Ingress.RedirectDomains) > 0 {
		redirectDomains = app.Spec.Ingress.RedirectDomains
	}

	redirectIngressName := fmt.Sprintf("%s-redirect", app.Name)

	// If no redirect domains, clean up any existing redirect resources
	if len(redirectDomains) == 0 {
		r.cleanupRedirectResources(ctx, app, target.Namespace, redirectIngressName)
		return nil
	}

	// Resolve the primary domain (redirect target): explicit redirectTarget → first domain → template → app-level
	var primaryDomain string
	if target.Config.Ingress != nil && target.Config.Ingress.RedirectTarget != "" {
		primaryDomain = target.Config.Ingress.RedirectTarget
	} else if app.Spec.Ingress != nil && app.Spec.Ingress.RedirectTarget != "" {
		primaryDomain = app.Spec.Ingress.RedirectTarget
	} else if target.Config.Ingress != nil && len(target.Config.Ingress.Domains) > 0 {
		primaryDomain = target.Config.Ingress.Domains[0]
	} else if target.Config.Ingress != nil && target.Config.Ingress.Domain != "" {
		primaryDomain = target.Config.Ingress.Domain
	} else if tpl := r.ConfigResolver.GetDomainTemplate(); tpl != "" && target.Config.Name != "" {
		expanded := strings.ReplaceAll(tpl, "{{app}}", app.Name)
		expanded = strings.ReplaceAll(expanded, "{{env}}", target.Config.Name)
		expanded = strings.ReplaceAll(expanded, "{{domain}}", r.ConfigResolver.GetDomain())
		primaryDomain = expanded
	} else if app.Spec.Ingress != nil && app.Spec.Ingress.Domain != "" {
		primaryDomain = app.Spec.Ingress.Domain
	}

	if primaryDomain == "" {
		logger.Info("no primary domain resolved for redirect, skipping", "app", app.Name)
		return nil
	}

	// Determine scheme based on TLS setting
	scheme := "http"
	tlsEnabled := false
	if app.Spec.Ingress != nil {
		tlsEnabled = app.Spec.Ingress.TLS
	}
	if target.Config.Ingress != nil && target.Config.Ingress.TLS != nil {
		tlsEnabled = *target.Config.Ingress.TLS
	}
	if tlsEnabled {
		scheme = "https"
	}
	redirectTarget := fmt.Sprintf("%s://%s", scheme, primaryDomain)

	// Resolve ingress class
	ingressClassName := r.resolveIngressClassName(ctx, app)

	labels := r.labelsForApp(app)
	labels["vesta.sh/redirect"] = "true"
	pathType := networkingv1.PathTypePrefix

	// Determine the service port for the ingress backend (same logic as main ingress)
	servicePort := int32(80)
	if app.Spec.Service != nil && len(app.Spec.Service.Ports) > 0 {
		servicePort = app.Spec.Service.Ports[0].Port
		for _, p := range app.Spec.Service.Ports {
			if p.Name == "http" {
				servicePort = p.Port
				break
			}
		}
	}

	// Resolve TLS config for redirect domains
	tlsSecretName := fmt.Sprintf("%s-redirect-tls", app.Name)
	if target.Config.Name != "" {
		tlsSecretName = fmt.Sprintf("%s-%s-redirect-tls", app.Name, target.Config.Name)
	}

	// For Traefik: create a Middleware CRD
	if strings.Contains(strings.ToLower(ingressClassName), "traefik") {
		if err := r.reconcileTraefikRedirectMiddleware(ctx, app, target.Namespace, redirectIngressName, primaryDomain, scheme); err != nil {
			logger.Error(err, "failed to reconcile traefik redirect middleware")
			// Fall through to create ingress anyway — annotations will reference the middleware
		}
	}

	return retry.OnError(retry.DefaultRetry, isRetriable, func() error {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      redirectIngressName,
				Namespace: target.Namespace,
			},
		}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
			ing.Labels = labels
			ing.Annotations = map[string]string{}

			// Apply ingress class
			if ingressClassName != "" {
				ing.Annotations["kubernetes.io/ingress.class"] = ingressClassName
			}

			// Apply controller-specific redirect annotations
			switch {
			case strings.Contains(strings.ToLower(ingressClassName), "nginx"):
				// NGINX Ingress Controller: native permanent-redirect
				ing.Annotations["nginx.ingress.kubernetes.io/permanent-redirect"] = redirectTarget + "/$request_uri"
				ing.Annotations["nginx.ingress.kubernetes.io/permanent-redirect-code"] = "301"

			case strings.Contains(strings.ToLower(ingressClassName), "traefik"):
				// Traefik: reference the Middleware we created
				middlewareRef := fmt.Sprintf("%s-%s@kubernetescrd", target.Namespace, redirectIngressName)
				ing.Annotations["traefik.ingress.kubernetes.io/router.middlewares"] = middlewareRef
			}

			// Cert-manager annotation for TLS on redirect domains
			var clusterIssuer string
			if app.Spec.Ingress != nil {
				clusterIssuer = app.Spec.Ingress.ClusterIssuer
			}
			if clusterIssuer == "" {
				clusterIssuer = r.ConfigResolver.GetClusterIssuer()
			}
			if clusterIssuer != "" && tlsEnabled {
				ing.Annotations["cert-manager.io/cluster-issuer"] = clusterIssuer
			}

			// Build ingress rules for redirect domains
			rules := make([]networkingv1.IngressRule, 0, len(redirectDomains))
			for _, d := range redirectDomains {
				if strings.TrimSpace(d) == "" {
					continue
				}
				rules = append(rules, networkingv1.IngressRule{
					Host: d,
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
												Number: servicePort,
											},
										},
									},
								},
							},
						},
					},
				})
			}

			existingClass := ing.Spec.IngressClassName

			ing.Spec = networkingv1.IngressSpec{
				Rules: rules,
			}

			switch {
			case ingressClassName != "":
				ing.Spec.IngressClassName = &ingressClassName
			case existingClass != nil:
				ing.Spec.IngressClassName = existingClass
			}

			if tlsEnabled {
				ing.Spec.TLS = []networkingv1.IngressTLS{
					{
						Hosts:      redirectDomains,
						SecretName: tlsSecretName,
					},
				}
			}

			return nil
		})
		return err
	})
}

// reconcileTraefikRedirectMiddleware creates/updates a Traefik Middleware CRD for domain redirects.
func (r *VestaAppReconciler) reconcileTraefikRedirectMiddleware(ctx context.Context, app *vestav1alpha1.VestaApp, namespace, name, primaryDomain, scheme string) error {
	middleware := &unstructured.Unstructured{}
	middleware.SetGroupVersionKind(traefikMiddlewareGVK())
	middleware.SetName(name)
	middleware.SetNamespace(namespace)
	middleware.SetLabels(r.labelsForApp(app))

	middleware.Object["spec"] = map[string]interface{}{
		"redirectRegex": map[string]interface{}{
			"regex":       "^https?://[^/]+(.*)",
			"replacement": fmt.Sprintf("%s://%s${1}", scheme, primaryDomain),
			"permanent":   true,
		},
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(traefikMiddlewareGVK())
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing)
	if errors.IsNotFound(err) {
		return r.Client.Create(ctx, middleware)
	} else if err != nil {
		return err
	}
	// Update existing
	existing.Object["spec"] = middleware.Object["spec"]
	existing.SetLabels(middleware.GetLabels())
	return r.Client.Update(ctx, existing)
}

// cleanupRedirectResources removes the redirect Ingress and Traefik Middleware if they exist.
func (r *VestaAppReconciler) cleanupRedirectResources(ctx context.Context, app *vestav1alpha1.VestaApp, namespace, redirectIngressName string) {
	logger := log.FromContext(ctx)

	// Delete redirect Ingress
	ing := &networkingv1.Ingress{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: redirectIngressName}, ing); err == nil {
		if err := r.Client.Delete(ctx, ing); err != nil {
			logger.Error(err, "failed to delete redirect ingress", "namespace", namespace, "name", redirectIngressName)
		}
	}

	// Delete Traefik Middleware if it exists
	mw := &unstructured.Unstructured{}
	mw.SetGroupVersionKind(traefikMiddlewareGVK())
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: redirectIngressName}, mw); err == nil {
		if err := r.Client.Delete(ctx, mw); err != nil {
			logger.Error(err, "failed to delete traefik redirect middleware", "namespace", namespace, "name", redirectIngressName)
		}
	}
}

func traefikMiddlewareGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   "traefik.io",
		Version: "v1alpha1",
		Kind:    "Middleware",
	}
}

// reconcileHTTPSRedirectMiddleware creates or deletes the HTTPS redirect middleware per app/env.
// When TLS is enabled and ingress class is Traefik, it creates a redirectScheme middleware.
// When TLS is not enabled, it cleans up any existing middleware.
func (r *VestaAppReconciler) reconcileHTTPSRedirectMiddleware(ctx context.Context, app *vestav1alpha1.VestaApp, target targetEnv) {
	logger := log.FromContext(ctx)
	middlewareName := fmt.Sprintf("%s-https-redirect", app.Name)

	// Determine if TLS is enabled for this env
	tlsEnabled := false
	if app.Spec.Ingress != nil {
		tlsEnabled = app.Spec.Ingress.TLS
	}
	if target.Config.Ingress != nil && target.Config.Ingress.TLS != nil {
		tlsEnabled = *target.Config.Ingress.TLS
	}

	// Determine ingress class
	ingressClassName := r.resolveIngressClassName(ctx, app)
	isTraefik := strings.Contains(strings.ToLower(ingressClassName), "traefik")

	if tlsEnabled && isTraefik {
		// Create/update the redirect middleware
		middleware := &unstructured.Unstructured{}
		middleware.SetGroupVersionKind(traefikMiddlewareGVK())
		middleware.SetName(middlewareName)
		middleware.SetNamespace(target.Namespace)
		middleware.SetLabels(r.labelsForApp(app))

		middleware.Object["spec"] = map[string]interface{}{
			"redirectScheme": map[string]interface{}{
				"scheme":    "https",
				"permanent": true,
			},
		}

		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(traefikMiddlewareGVK())
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: middlewareName}, existing)
		if errors.IsNotFound(err) {
			if err := r.Client.Create(ctx, middleware); err != nil {
				logger.Error(err, "failed to create HTTPS redirect middleware", "namespace", target.Namespace, "name", middlewareName)
			}
		} else if err == nil {
			existing.Object["spec"] = middleware.Object["spec"]
			existing.SetLabels(middleware.GetLabels())
			if err := r.Client.Update(ctx, existing); err != nil {
				logger.Error(err, "failed to update HTTPS redirect middleware", "namespace", target.Namespace, "name", middlewareName)
			}
		}
	} else {
		// Cleanup: delete the middleware if it exists (TLS was disabled or not Traefik)
		mw := &unstructured.Unstructured{}
		mw.SetGroupVersionKind(traefikMiddlewareGVK())
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: middlewareName}, mw); err == nil {
			if err := r.Client.Delete(ctx, mw); err != nil {
				logger.Error(err, "failed to delete HTTPS redirect middleware", "namespace", target.Namespace, "name", middlewareName)
			}
		}
	}
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

	// Build the set of desired cronjob names so we can clean up orphans.
	// Disabled cronjobs are still desired — they are kept around suspended.
	desiredCronJobs := map[string]bool{}
	for _, cj := range app.Spec.Cronjobs {
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

		// A disabled cronjob is still reconciled, but suspended so it never fires.
		// Keeping the object means schedule, history and manual triggers survive.
		suspend := !r.isCronjobEnabled(cj, target.Config.Name)

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

		// Auto-inject the per-app ConfigMap ("{appName}-envvars") as envFrom if it exists.
		appEnvVarsCM := app.Name + "-envvars"
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: appEnvVarsCM}, cm); err == nil {
			container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: appEnvVarsCM},
				},
			})
		}

		podSpec := r.buildPodSpec(app, container, projectPullSecrets, target.Config.ImagePullSecrets)
		podSpec.ServiceAccountName = app.Name
		switch cj.RestartPolicy {
		case "Never":
			podSpec.RestartPolicy = corev1.RestartPolicyNever
		default:
			podSpec.RestartPolicy = corev1.RestartPolicyOnFailure
		}

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
				ttl := int32(60) // cleanup finished job pods after 1 minute
				jobSpec := batchv1.JobSpec{
					TTLSecondsAfterFinished: &ttl,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: cronjobLabels,
						},
						Spec: podSpec,
					},
				}
				if cj.BackoffLimit != nil {
					jobSpec.BackoffLimit = cj.BackoffLimit
				}
				cronJob.Spec = batchv1.CronJobSpec{
					Schedule:                   effectiveSchedule,
					Suspend:                    &suspend,
					ConcurrencyPolicy:          batchv1.ForbidConcurrent,
					SuccessfulJobsHistoryLimit: &successLimit,
					FailedJobsHistoryLimit:     &failedLimit,
					JobTemplate: batchv1.JobTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: cronjobLabels,
						},
						Spec: jobSpec,
					},
				}
				return nil
			})
			return createErr
		})
		if err != nil {
			return fmt.Errorf("reconcile cronjob %s in %s: %w", cronjobName, target.Namespace, err)
		}

		logger.Info("cronjob reconciled", "namespace", target.Namespace, "cronjob", cronjobName, "suspended", suspend)
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

// isCronjobEnabled reports whether a cronjob should fire in a given environment.
// A per-environment override wins over the cronjob-level flag; both default to enabled.
func (r *VestaAppReconciler) isCronjobEnabled(cj vestav1alpha1.CronjobConfig, envName string) bool {
	for _, envOverride := range cj.Environments {
		if envOverride.Name == envName && envOverride.Enabled != nil {
			return *envOverride.Enabled
		}
	}
	if cj.Enabled != nil {
		return *cj.Enabled
	}
	return true
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
		var issues []appIssue

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
			issues = append(issues, diagnoseDeployment(target.Config.Name, &deploy)...)

			if deploy.Spec.Replicas != nil {
				totalDesired += *deploy.Spec.Replicas
			}
			totalAvailable += deploy.Status.AvailableReplicas
			totalReady += deploy.Status.ReadyReplicas

			// Check pods for CrashLoopBackOff / ImagePullBackOff
			// Exclude CronJob/Job pods — they have the "kubernetes.getvesta.sh/cronjob" label
			var podList corev1.PodList
			if err := r.List(ctx, &podList,
				client.InNamespace(target.Namespace),
				client.MatchingLabels{"kubernetes.getvesta.sh/app": app.Name},
			); err != nil {
				continue
			}

			issues = append(issues, diagnosePods(target.Config.Name, podList.Items)...)

			for _, pod := range podList.Items {
				// Skip pods that belong to cron jobs
				if _, isCronPod := pod.Labels["kubernetes.getvesta.sh/cronjob"]; isCronPod {
					continue
				}
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

		// Record why, not just what. A healthy app carries no reason; anything else
		// names the specific pod, container, and underlying error.
		reason, message := summarizeIssues(issues)
		switch app.Status.Phase {
		case "Running", "Sleeping":
			app.Status.Reason = ""
			app.Status.Message = ""
		default:
			app.Status.Reason = reason
			app.Status.Message = message
		}
		r.setReadyCondition(&app, reason, message)

		// Populate scaling status
		app.Status.Scaling = &vestav1alpha1.ScalingStatus{
			CurrentReplicas:  totalReady,
			DesiredReplicas:  totalDesired,
			AutoscalerActive: autoscaleActive,
		}

		// --- Existing metadata updates ---
		// Record deployment history per environment. Each environment resolves its
		// own image, so history has to be compared per environment — comparing a
		// single app-wide image would attribute one environment's tag to all of them.
		if app.Spec.Image != nil {
			lastImageByEnv := map[string]string{}
			for _, rec := range app.Status.DeploymentHistory {
				lastImageByEnv[rec.Environment] = rec.Image
			}

			nextVersion := 1
			if len(app.Status.DeploymentHistory) > 0 {
				nextVersion = app.Status.DeploymentHistory[len(app.Status.DeploymentHistory)-1].Version + 1
			}

			for _, target := range targetNamespaces {
				envName := target.Config.Name
				envImage := resolveImage(&app, target.Config.Image)
				if lastImageByEnv[envName] == envImage {
					continue
				}
				app.Status.DeploymentHistory = append(app.Status.DeploymentHistory, vestav1alpha1.DeploymentRecord{
					Version:     nextVersion,
					Image:       envImage,
					Environment: envName,
					DeployedAt:  now,
				})
				lastImageByEnv[envName] = envImage
				nextVersion++
				app.Status.CurrentImage = envImage
			}

			if app.Status.CurrentImage == "" && len(app.Status.DeploymentHistory) > 0 {
				app.Status.CurrentImage = app.Status.DeploymentHistory[len(app.Status.DeploymentHistory)-1].Image
			}
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
	reason, message := classifyReconcileError(reconcileErr)

	_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest vestav1alpha1.VestaApp
		if err := r.Get(ctx, client.ObjectKeyFromObject(app), &latest); err != nil {
			return err
		}
		latest.Status.Phase = "Failed"
		// The reconcile error used to be dropped here, leaving the bare word
		// "Failed" with the cause only in the operator's logs.
		latest.Status.Reason = reason
		latest.Status.Message = truncateMessage(message)
		r.setReadyCondition(&latest, reason, message)
		return r.Status().Update(ctx, &latest)
	})
	return ctrl.Result{}, reconcileErr
}

// setReadyCondition mirrors the reason onto a standard Ready condition so
// kubectl wait, kubectl describe, and controller-runtime tooling can read it.
func (r *VestaAppReconciler) setReadyCondition(app *vestav1alpha1.VestaApp, reason, message string) {
	ready := metav1.ConditionTrue
	condReason := "AppReady"
	condMessage := "All environments are running"

	if reason != "" {
		ready = metav1.ConditionFalse
		condReason = reason
		condMessage = truncateMessage(message)
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             ready,
		Reason:             condReason,
		Message:            condMessage,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: app.Generation,
	}

	for i, existing := range app.Status.Conditions {
		if existing.Type != condition.Type {
			continue
		}
		// Keep the original transition time when the state itself hasn't changed,
		// otherwise every reconcile looks like a fresh transition.
		if existing.Status == condition.Status {
			condition.LastTransitionTime = existing.LastTransitionTime
		}
		app.Status.Conditions[i] = condition
		return
	}
	app.Status.Conditions = append(app.Status.Conditions, condition)
}

// isRetriable returns true for errors that are safe to retry (conflicts and already-exists)
func isRetriable(err error) bool {
	return errors.IsConflict(err) || errors.IsAlreadyExists(err)
}

func (r *VestaAppReconciler) labelsForApp(app *vestav1alpha1.VestaApp) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":         app.Name,
		"app.kubernetes.io/managed-by":   "vesta-operator",
		"kubernetes.getvesta.sh/project": app.Spec.Project,
		"kubernetes.getvesta.sh/app":     app.Name,
	}
}

func (r *VestaAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaApp{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapEnvFromSourceToApps),
		).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.mapEnvFromSourceToApps),
		).
		Complete(r)
}

// mapEnvFromSourceToApps enqueues VestaApp reconciles when a Secret or ConfigMap
// that the operator may inject via envFrom is created/updated/deleted.
//
// Strategy: a per-app Secret is named "{appName}-secrets" and the per-app
// ConfigMap is "{appName}-envvars". The object's namespace is "{project}-{env}".
// We list VestaApps with .spec.project == project and pick the one whose name
// matches the suffix-stripped object name. We additionally enqueue any apps in
// that project that explicitly reference the object via spec.runtime.secrets.
func (r *VestaAppReconciler) mapEnvFromSourceToApps(ctx context.Context, obj client.Object) []reconcile.Request {
	objName := obj.GetName()
	objNS := obj.GetNamespace()

	// Namespace convention: "{project}-{env}". If it doesn't contain a dash we
	// can't infer the project, so skip the cheap path and just bail.
	dash := strings.LastIndex(objNS, "-")
	if dash <= 0 {
		return nil
	}
	project := objNS[:dash]

	// Determine whether this object is a per-app auto-injected source, and if
	// so what the app name is.
	var autoApp string
	switch {
	case strings.HasSuffix(objName, "-secrets"):
		autoApp = strings.TrimSuffix(objName, "-secrets")
	case strings.HasSuffix(objName, "-envvars"):
		autoApp = strings.TrimSuffix(objName, "-envvars")
	}

	// List apps in the same project (VestaApps live in the operator's
	// management namespace, not the workload namespace).
	apps := &vestav1alpha1.VestaAppList{}
	if err := r.List(ctx, apps, client.MatchingFields{}); err != nil {
		// Field selectors aren't indexed; fall back to a full list.
		apps = &vestav1alpha1.VestaAppList{}
		if err := r.List(ctx, apps); err != nil {
			return nil
		}
	}

	var requests []reconcile.Request
	seen := map[string]struct{}{}
	enqueue := func(a *vestav1alpha1.VestaApp) {
		key := a.Namespace + "/" + a.Name
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: a.Namespace, Name: a.Name}})
	}

	for i := range apps.Items {
		a := &apps.Items[i]
		if a.Spec.Project != project {
			continue
		}
		if autoApp != "" && a.Name == autoApp {
			enqueue(a)
			continue
		}
		// Explicit secret bindings (only Secrets — ConfigMaps aren't bound this way).
		if _, isSecret := obj.(*corev1.Secret); isSecret {
			for _, sb := range a.Spec.Runtime.Secrets {
				if sb.SecretRef != nil && sb.SecretRef.Name == objName {
					enqueue(a)
					break
				}
			}
		}
	}

	return requests
}

// computeRolloutHash combines the PodSpec hash with a fingerprint derived from
// the ResourceVersions of the Secrets and ConfigMaps that are referenced via
// envFrom. This ensures that updates to the underlying secret/configmap data
// — which are not reflected in the PodSpec itself — also trigger a rolling
// update of the Deployment.
func computeRolloutHash(spec corev1.PodSpec, fingerprintParts []string) string {
	h := sha256.New()
	data, _ := json.Marshal(spec)
	h.Write(data)
	// Sort to keep the hash stable regardless of iteration order.
	parts := append([]string(nil), fingerprintParts...)
	sort.Strings(parts)
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum[:8])
}

// shellSplit tokenizes a command string into argv the way a POSIX shell would
// for simple cases: whitespace separates tokens, and single or double quotes
// group tokens containing spaces. A backslash escapes the next character. It
// does not attempt to interpret shell metacharacters (pipes, variables, globs)
// — those only make sense under "/bin/sh -c" and are handled by the shell-form
// path in buildContainer. If the string yields no tokens it returns nil, which
// leaves the container entrypoint at the image default.
func shellSplit(s string) []string {
	var tokens []string
	var cur strings.Builder
	hasToken := false
	var quote rune // 0, '\'' or '"'
	escaped := false

	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
			hasToken = true
		case r == '\\' && quote != '\'':
			escaped = true
			hasToken = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			hasToken = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if hasToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				hasToken = false
			}
		default:
			cur.WriteRune(r)
			hasToken = true
		}
	}
	if hasToken {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
