package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

type VestaProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaprojects/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments,verbs=get;list;watch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaapps,verbs=get;list;watch

func (r *VestaProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var project vestav1alpha1.VestaProject
	if err := r.Get(ctx, req.NamespacedName, &project); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling VestaProject", "name", project.Name)

	if project.Labels == nil {
		project.Labels = map[string]string{}
	}
	updated := false
	if project.Labels["kubernetes.getvesta.sh/project"] != project.Name {
		project.Labels["kubernetes.getvesta.sh/project"] = project.Name
		updated = true
	}
	if project.Spec.Team != "" && project.Labels["kubernetes.getvesta.sh/team"] != project.Spec.Team {
		project.Labels["kubernetes.getvesta.sh/team"] = project.Spec.Team
		updated = true
	}
	if updated {
		if err := r.Update(ctx, &project); err != nil {
			return ctrl.Result{}, err
		}
	}

	var envList vestav1alpha1.VestaEnvironmentList
	if err := r.List(ctx, &envList, client.MatchingLabels{
		"kubernetes.getvesta.sh/project": project.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	var appList vestav1alpha1.VestaAppList
	if err := r.List(ctx, &appList, client.MatchingLabels{
		"kubernetes.getvesta.sh/project": project.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	project.Status.EnvironmentCount = len(envList.Items)
	project.Status.AppCount = len(appList.Items)
	if err := r.Status().Update(ctx, &project); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *VestaProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaProject{}).
		Complete(r)
}
