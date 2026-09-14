package activator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubeWaker is the cluster-backed Waker.
//
// It talks to the Kubernetes API with its own ServiceAccount token rather than to Vesta's
// API with a user credential. That is deliberate: there is no user behind an inbound request
// to a sleeping app, so there is no token to borrow, and inventing a shared secret to carry
// one would be a credential to rotate and leak. The audit trail is the apiserver's.
type KubeWaker struct {
	client    kubernetes.Interface
	namespace string

	// hosts caches the Host-to-app map built from the namespace's ingresses. Rebuilt on a
	// miss and on a short interval, because an app whose domain was just added should
	// become reachable without restarting this.
	mu        sync.RWMutex
	hosts     map[string]Target
	refreshed time.Time
	ttl       time.Duration
}

// NoWakePathsAnnotation carries the paths the activator answers itself, stamped on the
// app's Ingress by the operator.
//
// Read from the Ingress rather than from the VestaApp so the activator needs no access to
// Vesta's own custom resources beyond the patch it uses to wake one -- and so it learns
// about a change from the same object it already watches for routing.
const NoWakePathsAnnotation = "kubernetes.getvesta.sh/no-wake-paths"

func NewKubeWaker(namespace string) (*KubeWaker, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("this runs in-cluster only: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &KubeWaker{
		client: client, namespace: namespace,
		hosts: map[string]Target{}, ttl: 30 * time.Second,
	}, nil
}

// Wake patches spec.desiredState back to running.
//
// The instruction lives in spec, not status, which is what makes this possible at all: a
// patch to status would be discarded by the subresource, and the app would never come back.
func (k *KubeWaker) Wake(ctx context.Context, namespace, app string) error {
	patch := []byte(`{"spec":{"desiredState":"running"}}`)
	path := fmt.Sprintf("/apis/kubernetes.getvesta.sh/v1alpha1/namespaces/%s/vestaapps/%s",
		vestaSystemNamespace, app)

	_, err := k.client.Discovery().RESTClient().
		Patch(types.MergePatchType).
		AbsPath(path).
		Body(patch).
		DoRaw(ctx)
	return err
}

// Ready reports whether the app has a pod serving.
func (k *KubeWaker) Ready(ctx context.Context, namespace, app string) (bool, error) {
	deploy, err := k.client.AppsV1().Deployments(namespace).Get(ctx, app, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	return deploy.Status.ReadyReplicas >= 1, nil
}

// Resolve maps a Host header to the app that serves it.
//
// Built from the ingresses in this namespace, filtered to the ones the operator manages, so
// the activator never has to be told which domains exist -- it reads the same objects the
// ingress controller does.
func (k *KubeWaker) Resolve(ctx context.Context, host string) (Target, bool) {
	k.mu.RLock()
	fresh := time.Since(k.refreshed) < k.ttl
	target, ok := k.hosts[host]
	k.mu.RUnlock()

	if ok {
		return target, true
	}
	if fresh {
		// A miss against a current map is a genuine miss, not a stale one.
		return Target{}, false
	}

	if err := k.refresh(ctx); err != nil {
		return Target{}, false
	}

	k.mu.RLock()
	defer k.mu.RUnlock()
	target, ok = k.hosts[host]
	return target, ok
}

func (k *KubeWaker) refresh(ctx context.Context) error {
	list, err := k.client.NetworkingV1().Ingresses(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=vesta-operator",
	})
	if err != nil {
		return err
	}

	hosts := map[string]Target{}
	for i := range list.Items {
		ing := &list.Items[i]
		app := ing.Labels["kubernetes.getvesta.sh/app"]
		if app == "" {
			app = ing.Name
		}

		for _, rule := range ing.Spec.Rules {
			if rule.Host == "" || rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				svc := path.Backend.Service
				if svc == nil {
					continue
				}
				// The port recorded is the app's, not the one currently in the ingress:
				// while the app is asleep that backend is this activator, and proxying to
				// our own port would loop.
				hosts[strings.ToLower(rule.Host)] = Target{
					App:         app,
					Port:        k.appPort(ctx, app),
					NoWakePaths: splitPaths(ing.Annotations[NoWakePathsAnnotation]),
				}
			}
		}
	}

	k.mu.Lock()
	k.hosts = hosts
	k.refreshed = time.Now()
	k.mu.Unlock()
	return nil
}

// appPort finds the port the app's own Service listens on.
func (k *KubeWaker) appPort(ctx context.Context, app string) int32 {
	svc, err := k.client.CoreV1().Services(k.namespace).Get(ctx, app, metav1.GetOptions{})
	if err != nil || len(svc.Spec.Ports) == 0 {
		return 80
	}
	return svc.Spec.Ports[0].Port
}

// vestaSystemNamespace is where VestaApp objects live, regardless of which namespace the app
// itself runs in.
const vestaSystemNamespace = "vesta-system"

var _ Waker = (*KubeWaker)(nil)

// splitPaths reads the comma-separated annotation.
func splitPaths(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
