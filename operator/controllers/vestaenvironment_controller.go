package controllers

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

type VestaEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// ConfigResolver supplies platform-wide quota defaults and pod-size presets. Nil
	// disables both, which means quotas can still be set per project or environment but
	// nothing is inherited.
	ConfigResolver *ConfigResolver
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaapps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch

func (r *VestaEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env vestav1alpha1.VestaEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling VestaEnvironment", "name", env.Name, "project", env.Spec.Project)

	if env.Labels == nil {
		env.Labels = map[string]string{}
	}
	updated := false
	if env.Labels["kubernetes.getvesta.sh/project"] != env.Spec.Project {
		env.Labels["kubernetes.getvesta.sh/project"] = env.Spec.Project
		updated = true
	}
	if env.Labels["kubernetes.getvesta.sh/environment"] != env.Name {
		env.Labels["kubernetes.getvesta.sh/environment"] = env.Name
		updated = true
	}
	if updated {
		if err := r.Update(ctx, &env); err != nil {
			return ctrl.Result{}, err
		}
	}

	nsName := fmt.Sprintf("%s-%s", env.Spec.Project, env.Name)
	if err := r.ensureNamespace(ctx, &env, nsName); err != nil {
		return ctrl.Result{}, err
	}

	// Quotas, after the namespace exists to put them in. Reported on every pass whether or
	// not they are enforced, so the numbers are available before anyone commits to them.
	quotaStatus, err := r.reconcileQuota(ctx, &env, nsName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Network isolation, also after the namespace exists.
	isolation, err := r.reconcileNetworkPolicies(ctx, &env, nsName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var appList vestav1alpha1.VestaAppList
	if err := r.List(ctx, &appList, client.MatchingLabels{
		"kubernetes.getvesta.sh/project": env.Spec.Project,
	}); err != nil {
		return ctrl.Result{}, err
	}

	appCount := 0
	for _, app := range appList.Items {
		if len(app.Spec.Environments) == 0 {
			appCount++
			continue
		}
		for _, e := range app.Spec.Environments {
			if e.Name == env.Name {
				appCount++
				break
			}
		}
	}

	env.Status.AppCount = appCount
	env.Status.Quota = quotaStatus
	env.Status.NetworkIsolation = isolation
	if err := r.Status().Update(ctx, &env); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *VestaEnvironmentReconciler) ensureNamespace(ctx context.Context, env *vestav1alpha1.VestaEnvironment, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels["kubernetes.getvesta.sh/project"] = env.Spec.Project
		ns.Labels["kubernetes.getvesta.sh/environment"] = env.Name
		ns.Labels["app.kubernetes.io/managed-by"] = "vesta-operator"
		return nil
	})

	return err
}

func (r *VestaEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaEnvironment{}).
		Complete(r)
}

// reconcileQuota applies, or merely reports on, an environment's resource quota.
//
// Whether it applies anything is the user's explicit choice, because a ResourceQuota does
// not constrain pods that already exist -- it refuses the next admission. Set one too low
// and nothing happens until somebody deploys, at which point their rollout fails and it
// looks like a platform fault rather than a configuration one.
//
// So the numbers are worked out on every pass and published in status whether or not
// enforcement is on, and the UI can say "this would already be exceeded" before anyone
// commits to it.
func (r *VestaEnvironmentReconciler) reconcileQuota(ctx context.Context, env *vestav1alpha1.VestaEnvironment, namespace string) (*vestav1alpha1.QuotaStatus, error) {
	var project vestav1alpha1.VestaProject
	var projectQuota *vestav1alpha1.QuotaSpec
	if err := r.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: env.Spec.Project}, &project); err == nil {
		projectQuota = project.Spec.Quota
	}

	var platformQuota *vestav1alpha1.QuotaSpec
	if r.ConfigResolver != nil {
		if cfg := r.ConfigResolver.GetConfig(); cfg != nil {
			platformQuota = cfg.QuotaDefaults
		}
	}

	spec := ResolveQuota(platformQuota, projectQuota, env.Spec.Quota)
	if spec == nil {
		// Nothing configured anywhere. Remove whatever a previous configuration left, so
		// clearing a quota actually clears it.
		r.deleteQuotaObjects(ctx, namespace)
		return nil, nil
	}

	var apps vestav1alpha1.VestaAppList
	if err := r.List(ctx, &apps, client.InNamespace(env.Namespace),
		client.MatchingLabels{"kubernetes.getvesta.sh/project": env.Spec.Project}); err != nil {
		return nil, err
	}

	var resolveSize func(string) (corev1.ResourceList, corev1.ResourceList)
	if r.ConfigResolver != nil {
		resolveSize = r.ConfigResolver.ResolvePodSize
	}
	committed := CommittedResources(apps.Items, env.Name, resolveSize)

	status := &vestav1alpha1.QuotaStatus{Committed: ResourceListStrings(committed)}
	exceeds, reason := QuotaVerdict(spec, committed)
	status.WouldExceed = exceeds
	status.Reason = reason

	if !spec.Enforced() {
		status.Reason = strings.TrimSpace("reporting only; not enforced. " + reason)
		r.deleteQuotaObjects(ctx, namespace)
		return status, nil
	}

	// Refusing to apply a quota that is already exceeded is not the same as refusing the
	// configuration: the numbers are still reported, and scaling down to fit makes it apply
	// on the next pass. Applying it now would only mean the next deploy fails instead.
	if exceeds {
		status.Enforced = false
		status.Reason = "not applied: " + reason
		return status, nil
	}

	quota, err := BuildResourceQuota(namespace, spec)
	if err != nil {
		status.Reason = err.Error()
		return status, nil
	}
	if quota == nil {
		r.deleteQuotaObjects(ctx, namespace)
		return status, nil
	}

	// The LimitRange first, and always. Once a quota names requests.cpu, Kubernetes refuses
	// every pod that does not set one -- so creating the quota before the defaults exist
	// opens a window where any pod created in between is rejected.
	limits, err := BuildLimitRange(namespace, spec)
	if err != nil {
		status.Reason = err.Error()
		return status, nil
	}
	lr := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: limitRangeName, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, lr, func() error {
		lr.Labels = limits.Labels
		lr.Spec = limits.Spec
		return nil
	}); err != nil {
		return status, fmt.Errorf("limit range: %w", err)
	}

	rq := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: quotaName, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rq, func() error {
		rq.Labels = quota.Labels
		rq.Spec = quota.Spec
		return nil
	}); err != nil {
		return status, fmt.Errorf("resource quota: %w", err)
	}

	status.Enforced = true
	status.Used = ResourceListStrings(rq.Status.Used)
	return status, nil
}

// deleteQuotaObjects removes a quota that is no longer configured or no longer enforced.
//
// The LimitRange goes with it. Left behind it would keep injecting defaults into pods that
// never asked for them, which is a surprising thing for a removed quota to still be doing.
func (r *VestaEnvironmentReconciler) deleteQuotaObjects(ctx context.Context, namespace string) {
	for _, obj := range []client.Object{
		&corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: quotaName, Namespace: namespace}},
		&corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: limitRangeName, Namespace: namespace}},
	} {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "removing quota object", "namespace", namespace)
		}
	}
}

// reconcileNetworkPolicies isolates an environment's namespace.
//
// The honesty problem is the whole job here. NetworkPolicy is enforced by the cluster's
// network plugin, not by Kubernetes: a cluster running flannel accepts every policy, lists
// them back, and filters nothing. An operator who turns this on and sees the objects created
// would reasonably conclude their environments are separated when no packet is being
// stopped -- so what was actually concluded about this cluster is reported alongside.
func (r *VestaEnvironmentReconciler) reconcileNetworkPolicies(
	ctx context.Context, env *vestav1alpha1.VestaEnvironment, namespace string,
) (*vestav1alpha1.NetworkIsolationStatus, error) {

	logger := log.FromContext(ctx)

	var cfg NetworkIsolation
	if c := r.ConfigResolver.GetConfig(); c != nil && c.Security != nil && c.Security.NetworkIsolation != nil {
		ni := c.Security.NetworkIsolation
		cfg = NetworkIsolation{
			Enabled:                ni.Enabled,
			TrustedNamespaces:      ni.TrustedNamespaces,
			TrustedNamespaceLabels: ni.TrustedNamespaceLabels,
			MetricsPort:            ni.MetricsPort,
		}
	}

	desired := BuildNetworkPolicies(namespace, cfg)

	// Everything Vesta owns in this namespace, so turning isolation off removes the policies
	// rather than leaving a default-deny behind that nothing will ever clean up. That failure
	// would take every app in the environment offline with no object left to explain why.
	var existing networkingv1.NetworkPolicyList
	if err := r.List(ctx, &existing,
		client.InNamespace(namespace),
		client.MatchingLabels{"app.kubernetes.io/managed-by": "vesta-operator"},
	); err != nil {
		return nil, err
	}

	wanted := map[string]bool{}
	for _, p := range desired {
		wanted[p.Name] = true
	}
	for i := range existing.Items {
		p := &existing.Items[i]
		if wanted[p.Name] {
			continue
		}
		if err := r.Delete(ctx, p); err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
	}

	for _, p := range desired {
		policy := p
		spec := policy.Spec
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
			policy.Spec = spec
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if !cfg.Enabled {
		return nil, nil
	}

	status := &vestav1alpha1.NetworkIsolationStatus{
		Enabled:     true,
		PolicyCount: int32(len(desired)),
		TrustedFrom: cfg.TrustedNamespaces,
		LastApplied: metav1.Now(),
	}

	// Ask the cluster what CNI it runs. A failure to look is reported as unknown rather
	// than as enforcement, since assuming the safe-sounding answer is the mistake.
	var daemons appsv1.DaemonSetList
	if err := r.List(ctx, &daemons); err != nil {
		status.Note = "could not inspect the cluster's network plugin: " + err.Error()
		logger.V(1).Info("network policy: CNI inspection failed", "error", err)
		return status, nil
	}

	names := make([]string, 0, len(daemons.Items))
	for i := range daemons.Items {
		names = append(names, daemons.Items[i].Name)
	}

	enforces, known, note := CNIEnforcesNetworkPolicy(names)
	status.Enforced = enforces
	status.EnforcementKnown = known
	status.Note = note

	if known && !enforces {
		// Loud, because this is the case where the feature looks like it is working and is
		// not doing anything at all.
		logger.Info("network policies were created but this cluster's CNI does not enforce them",
			"namespace", namespace, "note", note)
	}

	return status, nil
}
