package controllers

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// A removed domain has to take its Ingress with it. reconcileIngress used to return early
// when no domain resolved, so an app that kept an ingress block (just a certificate issuer)
// went on serving the old host indefinitely.
func TestDeleteAppIngressRemovesTheIngress(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = vestav1alpha1.AddToScheme(scheme)

	existing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop-staging"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	r := &VestaAppReconciler{Client: c, Scheme: scheme}

	app := &vestav1alpha1.VestaApp{ObjectMeta: metav1.ObjectMeta{Name: "web"}}
	target := targetEnv{Namespace: "shop-staging"}

	if err := r.deleteAppIngress(context.Background(), app, target); err != nil {
		t.Fatalf("delete: %v", err)
	}
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "shop-staging", Name: "web"}, &networkingv1.Ingress{})
	if err == nil {
		t.Fatal("the Ingress is still there")
	}
	// And again with nothing to delete: not an error.
	if err := r.deleteAppIngress(context.Background(), app, target); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestFirstEnvironmentURL(t *testing.T) {
	yes := true
	app := &vestav1alpha1.VestaApp{Spec: vestav1alpha1.VestaAppSpec{
		Environments: []vestav1alpha1.AppEnvironmentConfig{
			{Name: "dev"},
			{Name: "staging", Ingress: &vestav1alpha1.IngressOverride{Domains: []string{"admin.example.com", "b.example.com"}, TLS: &yes}},
		},
	}}
	if got := firstEnvironmentURL(app); got != "https://admin.example.com" {
		t.Errorf("got %q", got)
	}
	if got := firstEnvironmentURL(&vestav1alpha1.VestaApp{}); got != "" {
		t.Errorf("no environments: got %q", got)
	}
}
