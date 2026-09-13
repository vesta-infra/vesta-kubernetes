package controllers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// VestaMiddlewareReconciler projects a VestaMiddleware into every namespace that has an
// app referencing it.
//
// The projection exists because a Traefik middleware reference is namespace-qualified:
// an Ingress in namespace "shop-production" can only name a Middleware in that namespace.
// A middleware meant to be defined once and reused therefore cannot be a single object --
// it has to be copied into each namespace that uses it, and withdrawn from the ones that
// stop.
type VestaMiddlewareReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// RESTMapper discovers whether the Traefik Middleware CRD is installed at all.
	Mapper meta.RESTMapper
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestamiddlewares,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestamiddlewares/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=traefik.io,resources=middlewares,verbs=get;list;watch;create;update;patch;delete

const middlewareOfLabel = "kubernetes.getvesta.sh/middleware"

func (r *VestaMiddlewareReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var mw vestav1alpha1.VestaMiddleware
	if err := r.Get(ctx, req.NamespacedName, &mw); err != nil {
		if errors.IsNotFound(err) {
			// Deleted. The projections carry an owner reference only when they live in the
			// same namespace, which they generally do not, so they are cleaned up by name.
			return ctrl.Result{}, r.deleteProjectionsEverywhere(ctx, req.Name)
		}
		return ctrl.Result{}, err
	}

	// A missing Traefik CRD is a configuration fact about the cluster, not a transient
	// error: requeuing forever would bury the real reason in reconcile noise.
	if !r.traefikInstalled() {
		return ctrl.Result{RequeueAfter: 10 * time.Minute}, r.setStatus(ctx, &mw, false,
			"Traefik CRD traefik.io/v1alpha1 Middleware is not installed in this cluster", nil)
	}

	compiled, err := compileMiddleware(mw.Spec)
	if err != nil {
		// A spec that cannot compile will not compile on a retry either. Record why and
		// wait for the author to change it.
		logger.Info("middleware spec is invalid", "name", mw.Name, "reason", err.Error())
		return ctrl.Result{}, r.setStatus(ctx, &mw, false, err.Error(), nil)
	}

	wanted, err := r.namespacesReferencing(ctx, mw.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, ns := range wanted {
		if err := r.project(ctx, &mw, ns, compiled); err != nil {
			return ctrl.Result{}, fmt.Errorf("project middleware %s into %s: %w", mw.Name, ns, err)
		}
	}

	if err := r.garbageCollect(ctx, mw.Name, wanted); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.setStatus(ctx, &mw, true, "", wanted)
}

// namespacesReferencing finds every namespace whose apps name this middleware. It reads
// the apps rather than a stored list on the middleware, so an app edited directly with
// kubectl is accounted for the same as one edited through the API.
func (r *VestaMiddlewareReconciler) namespacesReferencing(ctx context.Context, name string) ([]string, error) {
	var apps vestav1alpha1.VestaAppList
	if err := r.List(ctx, &apps); err != nil {
		return nil, fmt.Errorf("list apps: %w", err)
	}

	set := map[string]bool{}
	for i := range apps.Items {
		app := &apps.Items[i]
		for _, env := range appEnvironments(app) {
			for _, ref := range resolveAppMiddlewares(app, env) {
				if strings.TrimSpace(ref) == name {
					set[fmt.Sprintf("%s-%s", app.Spec.Project, env.Name)] = true
				}
			}
		}
	}

	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out, nil
}

// appEnvironments lists an app's environments. An app with no explicit environments
// inherits its project's, but resolving those needs a project lookup the reconciler can
// skip: an app that declares no environments also declares no per-environment middleware
// override, so the app-level list applies to whatever environments exist. Returning the
// declared ones is therefore exact whenever middlewares are involved.
func appEnvironments(app *vestav1alpha1.VestaApp) []vestav1alpha1.AppEnvironmentConfig {
	return app.Spec.Environments
}

func (r *VestaMiddlewareReconciler) project(ctx context.Context, mw *vestav1alpha1.VestaMiddleware, namespace string, compiled map[string]interface{}) error {
	name := projectedMiddlewareName(mw.Name)

	desired := &unstructured.Unstructured{}
	desired.SetGroupVersionKind(traefikMiddlewareGVK())
	desired.SetName(name)
	desired.SetNamespace(namespace)
	desired.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": "vesta-operator",
		middlewareOfLabel:              mw.Name,
	})
	desired.Object["spec"] = compiled

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(traefikMiddlewareGVK())
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Object["spec"] = compiled
	existing.SetLabels(desired.GetLabels())
	return r.Update(ctx, existing)
}

// garbageCollect removes projections in namespaces that no longer reference the middleware.
// It selects on the label rather than guessing namespaces, so a projection left behind by
// an app that has since been deleted is still found.
func (r *VestaMiddlewareReconciler) garbageCollect(ctx context.Context, name string, keep []string) error {
	keepSet := make(map[string]bool, len(keep))
	for _, ns := range keep {
		keepSet[ns] = true
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(traefikMiddlewareListGVK())
	if err := r.List(ctx, list, client.MatchingLabels{middlewareOfLabel: name}); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("list projections of %s: %w", name, err)
	}

	for i := range list.Items {
		item := &list.Items[i]
		if keepSet[item.GetNamespace()] {
			continue
		}
		if err := r.Delete(ctx, item); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete projection %s/%s: %w", item.GetNamespace(), item.GetName(), err)
		}
	}
	return nil
}

func (r *VestaMiddlewareReconciler) deleteProjectionsEverywhere(ctx context.Context, name string) error {
	return r.garbageCollect(ctx, name, nil)
}

func (r *VestaMiddlewareReconciler) traefikInstalled() bool {
	if r.Mapper == nil {
		return true
	}
	gvk := traefikMiddlewareGVK()
	_, err := r.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	return err == nil
}

func (r *VestaMiddlewareReconciler) setStatus(ctx context.Context, mw *vestav1alpha1.VestaMiddleware, ready bool, reason string, namespaces []string) error {
	mw.Status.Ready = ready
	mw.Status.Reason = reason
	mw.Status.AppliedNamespaces = namespaces
	mw.Status.AppliedCount = len(namespaces)
	mw.Status.ObservedGeneration = mw.Generation
	mw.Status.LastSyncedAt = time.Now().UTC().Format(time.RFC3339)
	return r.Status().Update(ctx, mw)
}

func traefikMiddlewareListGVK() schema.GroupVersionKind {
	gvk := traefikMiddlewareGVK()
	gvk.Kind += "List"
	return gvk
}

func (r *VestaMiddlewareReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaMiddleware{}).
		// An app changing its middleware list changes which namespaces need projections,
		// and the middleware itself does not change when that happens -- so without this
		// watch a newly attached middleware would not appear until something else forced a
		// reconcile.
		Watches(
			&vestav1alpha1.VestaApp{},
			handler.EnqueueRequestsFromMapFunc(r.middlewaresReferencedBy),
		).
		Complete(r)
}

func (r *VestaMiddlewareReconciler) middlewaresReferencedBy(ctx context.Context, obj client.Object) []ctrl.Request {
	app, ok := obj.(*vestav1alpha1.VestaApp)
	if !ok {
		return nil
	}

	seen := map[string]bool{}
	var reqs []ctrl.Request
	for _, env := range appEnvironments(app) {
		for _, ref := range resolveAppMiddlewares(app, env) {
			ref = strings.TrimSpace(ref)
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			reqs = append(reqs, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: ref, Namespace: app.Namespace},
			})
		}
	}
	return reqs
}
