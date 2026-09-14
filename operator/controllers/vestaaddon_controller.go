package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// VestaAddonReconciler runs managed datastores.
//
// spec.addons on VestaApp was accepted and stored for a long time with nothing reconciling
// it, so declaring an add-on did nothing at all. This is what makes it real.
//
// It follows the projection pattern rather than owner references: the VestaAddon lives in
// vesta-system while its StatefulSet, Service and Secret live in {project}-{env}, and an
// owner reference cannot cross namespaces. Children are found by label, and a finalizer
// holds deletion open long enough to decide what happens to the data.
type VestaAddonReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ConfigResolver *ConfigResolver
}

const (
	addonFinalizer     = "kubernetes.getvesta.sh/addon-cleanup"
	addonHomeNamespace = "vesta-system"
)

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaaddons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaaddons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaaddons/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete

func (r *VestaAddonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var addon vestav1alpha1.VestaAddon
	if err := r.Get(ctx, req.NamespacedName, &addon); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !addon.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &addon)
	}

	if !controllerutil.ContainsFinalizer(&addon, addonFinalizer) {
		controllerutil.AddFinalizer(&addon, addonFinalizer)
		if err := r.Update(ctx, &addon); err != nil {
			return ctrl.Result{}, err
		}
		// The update re-triggers; doing the work now would race with it.
		return ctrl.Result{}, nil
	}

	namespaces, err := r.targetNamespaces(ctx, &addon)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(namespaces) == 0 {
		// The project has no environments yet. Not an error -- an add-on declared before
		// the first environment is created is a reasonable order to do things in.
		if err := r.setStatus(ctx, &addon, false, "waiting for an environment to exist", nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	ready := 0
	for _, ns := range namespaces {
		if err := r.reconcileNamespace(ctx, &addon, ns); err != nil {
			return ctrl.Result{}, r.setStatus(ctx, &addon, false, err.Error(), namespaces)
		}
		if r.statefulSetReady(ctx, addon.Name, ns) {
			ready++
		}
	}

	if err := r.garbageCollect(ctx, &addon, namespaces); err != nil {
		log.FromContext(ctx).Error(err, "collecting addon objects from namespaces it no longer covers")
	}

	reason := ""
	if ready < len(namespaces) {
		reason = fmt.Sprintf("%d of %d instances ready", ready, len(namespaces))
	}
	if err := r.setStatus(ctx, &addon, ready == len(namespaces), reason, namespaces); err != nil {
		return ctrl.Result{}, err
	}

	// A datastore becomes ready on its own schedule, with nothing in the cluster changing
	// to say so.
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// targetNamespaces returns the namespaces this add-on runs in.
func (r *VestaAddonReconciler) targetNamespaces(ctx context.Context, addon *vestav1alpha1.VestaAddon) ([]string, error) {
	if addon.Spec.Environment != "" {
		return []string{fmt.Sprintf("%s-%s", addon.Spec.Project, addon.Spec.Environment)}, nil
	}

	var envs vestav1alpha1.VestaEnvironmentList
	if err := r.List(ctx, &envs, client.InNamespace(addonHomeNamespace),
		client.MatchingLabels{"kubernetes.getvesta.sh/project": addon.Spec.Project}); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(envs.Items))
	for _, e := range envs.Items {
		out = append(out, fmt.Sprintf("%s-%s", addon.Spec.Project, e.Name))
	}
	sort.Strings(out)
	return out, nil
}

// reconcileNamespace brings one instance of the add-on up to date.
//
// Order matters: the Secret is written first, because the StatefulSet's environment is built
// from the credentials in it. Reversing that would start a database with one password and
// hand the app another.
func (r *VestaAddonReconciler) reconcileNamespace(ctx context.Context, addon *vestav1alpha1.VestaAddon, namespace string) error {
	creds, err := r.ensureCredentials(ctx, addon, namespace)
	if err != nil {
		return err
	}

	svc, err := BuildAddonService(addon, namespace)
	if err != nil {
		return err
	}
	existingSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: svc.Name, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, existingSvc, func() error {
		existingSvc.Labels = svc.Labels
		// ClusterIP is immutable once assigned, so only the mutable parts are written.
		existingSvc.Spec.ClusterIP = svc.Spec.ClusterIP
		existingSvc.Spec.Selector = svc.Spec.Selector
		existingSvc.Spec.Ports = svc.Spec.Ports
		return nil
	}); err != nil {
		return fmt.Errorf("service in %s: %w", namespace, err)
	}

	requests, limits := r.resourcesFor(addon)
	desired, err := BuildAddonStatefulSet(addon, namespace, creds, requests, limits)
	if err != nil {
		return err
	}

	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Labels = desired.Labels
		// Selector and volumeClaimTemplates are immutable on a StatefulSet, so they are
		// only set on create. Writing them on update makes the API server reject every
		// reconcile, which reads as the add-on being permanently broken.
		if sts.CreationTimestamp.IsZero() {
			sts.Spec.Selector = desired.Spec.Selector
			sts.Spec.VolumeClaimTemplates = desired.Spec.VolumeClaimTemplates
			sts.Spec.ServiceName = desired.Spec.ServiceName
		}
		sts.Spec.Replicas = desired.Spec.Replicas
		sts.Spec.Template = desired.Spec.Template
		return nil
	}); err != nil {
		return fmt.Errorf("statefulset in %s: %w", namespace, err)
	}
	return nil
}

// ensureCredentials reads or creates the connection Secret.
func (r *VestaAddonReconciler) ensureCredentials(ctx context.Context, addon *vestav1alpha1.VestaAddon, namespace string) (AddonCredentials, error) {
	name := AddonSecretName(addon.Name)

	var existing corev1.Secret
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return AddonCredentials{}, err
	}

	// The stored password is passed in so it is kept. Generating a new one here would
	// rotate the secret under a running database that still has the old one.
	creds, err := BuildCredentials(addon, namespace, existing.Data)
	if err != nil {
		return AddonCredentials{}, err
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Labels = map[string]string{
			"app.kubernetes.io/managed-by": "vesta-operator",
			"app.kubernetes.io/component":  "addon",
			addonLabel:                     addon.Name,
		}
		secret.Type = corev1.SecretTypeOpaque
		secret.StringData = CredentialData(addon.Spec.Type, creds)
		return nil
	}); err != nil {
		return AddonCredentials{}, fmt.Errorf("credentials in %s: %w", namespace, err)
	}
	return creds, nil
}

func (r *VestaAddonReconciler) resourcesFor(addon *vestav1alpha1.VestaAddon) (corev1.ResourceList, corev1.ResourceList) {
	if addon.Spec.Size == "" || r.ConfigResolver == nil {
		return nil, nil
	}
	return r.ConfigResolver.ResolvePodSize(addon.Spec.Size)
}

func (r *VestaAddonReconciler) statefulSetReady(ctx context.Context, name, namespace string) bool {
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &sts); err != nil {
		return false
	}
	return sts.Status.ReadyReplicas >= 1
}

// garbageCollect removes instances from namespaces the add-on no longer covers.
//
// Found by label across every namespace rather than by guessing which ones it used to be in:
// an environment that was deleted takes its namespace with it, and an add-on narrowed from
// every environment to one leaves instances nothing else would find.
//
// The PVC is deliberately not deleted here. Narrowing an add-on's scope is a configuration
// change, not a request to destroy data.
func (r *VestaAddonReconciler) garbageCollect(ctx context.Context, addon *vestav1alpha1.VestaAddon, keep []string) error {
	wanted := map[string]bool{}
	for _, ns := range keep {
		wanted[ns] = true
	}

	var sets appsv1.StatefulSetList
	if err := r.List(ctx, &sets, client.MatchingLabels{addonLabel: addon.Name}); err != nil {
		return err
	}
	for i := range sets.Items {
		s := &sets.Items[i]
		if wanted[s.Namespace] {
			continue
		}
		if err := r.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: addon.Name, Namespace: s.Namespace}})
	}
	return nil
}

// finalize decides what happens to the data, then lets the object go.
func (r *VestaAddonReconciler) finalize(ctx context.Context, addon *vestav1alpha1.VestaAddon) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(addon, addonFinalizer) {
		return ctrl.Result{}, nil
	}

	retain := RetainOnDelete(addon)
	var retained []string

	var sets appsv1.StatefulSetList
	if err := r.List(ctx, &sets, client.MatchingLabels{addonLabel: addon.Name}); err != nil {
		return ctrl.Result{}, err
	}

	for i := range sets.Items {
		s := &sets.Items[i]
		if err := r.Delete(ctx, s); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: addon.Name, Namespace: s.Namespace}})

		// A StatefulSet's claims outlive it by default, which is the behaviour wanted
		// under Retain and has to be undone explicitly under Delete.
		var claims corev1.PersistentVolumeClaimList
		if err := r.List(ctx, &claims, client.InNamespace(s.Namespace),
			client.MatchingLabels{addonLabel: addon.Name}); err != nil {
			return ctrl.Result{}, err
		}
		for j := range claims.Items {
			pvc := &claims.Items[j]
			if retain {
				retained = append(retained, pvc.Namespace+"/"+pvc.Name)
				continue
			}
			if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}

		// Credentials go with the data: keeping a password for a volume nobody can find is
		// no use, and deleting one for a volume being kept makes the data unreadable.
		if !retain {
			_ = r.Delete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: AddonSecretName(addon.Name), Namespace: s.Namespace},
			})
		}
	}

	if len(retained) > 0 {
		sort.Strings(retained)
		log.FromContext(ctx).Info("addon deleted, data retained", "claims", retained)
	}

	controllerutil.RemoveFinalizer(addon, addonFinalizer)
	return ctrl.Result{}, r.Update(ctx, addon)
}

func (r *VestaAddonReconciler) setStatus(ctx context.Context, addon *vestav1alpha1.VestaAddon,
	ready bool, reason string, namespaces []string) error {

	status := vestav1alpha1.VestaAddonStatus{
		Ready:              ready,
		Reason:             reason,
		SecretName:         AddonSecretName(addon.Name),
		Namespaces:         namespaces,
		ObservedGeneration: addon.Generation,
	}
	if ready {
		status.Phase = "Running"
	} else {
		status.Phase = "Pending"
	}

	// Guarded so a reconcile that changed nothing does not write, which would re-trigger
	// itself every thirty seconds forever.
	if addon.Status.Ready == status.Ready &&
		addon.Status.Reason == status.Reason &&
		addon.Status.Phase == status.Phase &&
		addon.Status.ObservedGeneration == status.ObservedGeneration &&
		sameStrings(addon.Status.Namespaces, status.Namespaces) {
		return nil
	}

	addon.Status = status
	return r.Status().Update(ctx, addon)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *VestaAddonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaAddon{}).
		Complete(r)
}
