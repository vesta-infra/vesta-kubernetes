package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestaapps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch

func (r *VestaEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var env vestav1alpha1.VestaEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		if errors.IsNotFound(err) {
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
