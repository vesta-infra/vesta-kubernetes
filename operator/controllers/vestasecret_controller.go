package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

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

type VestaSecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestasecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubernetes.getvesta.sh,resources=vestasecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *VestaSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var vs vestav1alpha1.VestaSecret
	if err := r.Get(ctx, req.NamespacedName, &vs); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling VestaSecret", "name", vs.Name, "namespace", vs.Namespace)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vs.Name,
			Namespace: vs.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Labels = map[string]string{
			"app.kubernetes.io/managed-by":       "vesta-operator",
			"kubernetes.getvesta.sh/project":     vs.Spec.Project,
			"kubernetes.getvesta.sh/app":         vs.Spec.App,
			"kubernetes.getvesta.sh/environment": vs.Spec.Environment,
		}
		secret.Type = corev1.SecretType(vs.Spec.Type)

		switch vs.Spec.Type {
		case "Opaque":
			secret.Data = make(map[string][]byte)
			for k, v := range vs.Spec.Data {
				secret.Data[k] = []byte(v)
			}

		case "kubernetes.io/dockerconfigjson":
			if vs.Spec.DockerConfig != nil {
				dockerJSON, err := buildDockerConfigJSON(vs.Spec.DockerConfig)
				if err != nil {
					return fmt.Errorf("build docker config: %w", err)
				}
				secret.Data = map[string][]byte{
					".dockerconfigjson": dockerJSON,
				}
			}

		case "kubernetes.io/tls":
			if vs.Spec.TLS != nil {
				secret.Data = map[string][]byte{
					"tls.crt": []byte(vs.Spec.TLS.Cert),
					"tls.key": []byte(vs.Spec.TLS.Key),
				}
			}
		}

		return controllerutil.SetControllerReference(&vs, secret, r.Scheme)
	})

	if err != nil {
		vs.Status.Synced = false
		_ = r.Status().Update(ctx, &vs)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	vs.Status.Synced = true
	vs.Status.LastSyncedAt = time.Now().UTC().Format(time.RFC3339)
	vs.Status.SecretName = secret.Name
	if err := r.Status().Update(ctx, &vs); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func buildDockerConfigJSON(dc *vestav1alpha1.DockerSecretConfig) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", dc.Username, dc.Password)))
	config := map[string]interface{}{
		"auths": map[string]interface{}{
			dc.Registry: map[string]string{
				"username": dc.Username,
				"password": dc.Password,
				"auth":     auth,
			},
		},
	}
	return json.Marshal(config)
}

func (r *VestaSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vestav1alpha1.VestaSecret{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
