package controllers

import (
	"context"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"kubernetes.getvesta.sh/operator/activator"
	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func twoPortApp() *vestav1alpha1.VestaApp {
	return &vestav1alpha1.VestaApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: vestav1alpha1.VestaAppSpec{
			Service: &vestav1alpha1.ServiceConfig{Ports: []vestav1alpha1.ServicePort{
				{Name: "http", Port: 80, TargetPort: 3000},
				{Name: "admin", Port: 9000, TargetPort: 9000},
			}},
			Ingress: &vestav1alpha1.IngressConfig{
				Domain:      "app.example.com",
				DomainPorts: map[string]string{"admin.example.com": "admin"},
			},
		},
	}
}

func TestResolveDomainPorts(t *testing.T) {
	app := twoPortApp()
	target := targetEnv{Config: vestav1alpha1.AppEnvironmentConfig{Name: "prod"}}

	got, err := resolveDomainPorts(app, target, []string{"app.example.com", "Admin.Example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got["app.example.com"] != 80 || got["Admin.Example.com"] != 9000 {
		t.Errorf("got %v", got)
	}

	// An environment map replaces the app-level one, it does not merge.
	target.Config.Ingress = &vestav1alpha1.IngressOverride{DomainPorts: map[string]string{"app.example.com": "admin"}}
	got, err = resolveDomainPorts(app, target, []string{"app.example.com", "admin.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got["app.example.com"] != 9000 || got["admin.example.com"] != 80 {
		t.Errorf("env override: got %v", got)
	}

	// Naming a port the Service lacks is an error, not a fall back to the default port.
	target.Config.Ingress.DomainPorts = map[string]string{"app.example.com": "metrics"}
	if _, err := resolveDomainPorts(app, target, []string{"app.example.com"}); err == nil {
		t.Error("expected an error for an undeclared port name")
	}
}

// An environment that overrides service ports is what its Service is built from, so the
// default ingress port has to come from there too.
func TestDefaultPortFollowsEnvironmentServicePorts(t *testing.T) {
	app := twoPortApp()
	app.Spec.Ingress.DomainPorts = nil
	target := targetEnv{Config: vestav1alpha1.AppEnvironmentConfig{
		Service: &vestav1alpha1.ServiceOverride{Ports: []vestav1alpha1.ServicePort{{Name: "web", Port: 8080}}},
	}}
	got, err := resolveDomainPorts(app, target, []string{"app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got["app.example.com"] != 8080 {
		t.Errorf("got %v", got)
	}
}

func reconcileTwoDomainIngress(t *testing.T, app *vestav1alpha1.VestaApp) *networkingv1.Ingress {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = vestav1alpha1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &VestaAppReconciler{Client: c, Scheme: scheme, ConfigResolver: &ConfigResolver{}}

	target := targetEnv{Namespace: "shop-prod", Config: vestav1alpha1.AppEnvironmentConfig{
		Name:    "prod",
		Ingress: &vestav1alpha1.IngressOverride{Domains: []string{"app.example.com", "admin.example.com"}},
	}}
	if err := r.reconcileIngress(context.Background(), app, target); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	ing := &networkingv1.Ingress{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "shop-prod", Name: "web"}, ing); err != nil {
		t.Fatalf("get ingress: %v", err)
	}
	return ing
}

func backendPorts(ing *networkingv1.Ingress) map[string]int32 {
	out := map[string]int32{}
	for _, rule := range ing.Spec.Rules {
		out[rule.Host] = rule.HTTP.Paths[0].Backend.Service.Port.Number
	}
	return out
}

func TestIngressRoutesEachDomainToItsPort(t *testing.T) {
	app := twoPortApp()
	app.Spec.DesiredState = "running"
	ing := reconcileTwoDomainIngress(t, app)

	// Wake-on-traffic is off by default, so the backend is the app itself.
	if name := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name; name != "web" {
		t.Fatalf("backend service = %q, want the app", name)
	}
	if got := backendPorts(ing); got["app.example.com"] != 80 || got["admin.example.com"] != 9000 {
		t.Errorf("backend ports: %v", got)
	}
	if want := "admin.example.com=9000,app.example.com=80"; ing.Annotations[activator.HostPortsAnnotation] != want {
		t.Errorf("host-ports annotation = %q, want %q", ing.Annotations[activator.HostPortsAnnotation], want)
	}
}

func TestSleepingIngressSendsEveryDomainToTheActivator(t *testing.T) {
	app := twoPortApp()
	app.Spec.DesiredState = "stopped"
	wake := true
	app.Spec.Sleep = &vestav1alpha1.SleepConfig{Enabled: true, WakeOnTraffic: &wake}
	ing := reconcileTwoDomainIngress(t, app)

	for host, port := range backendPorts(ing) {
		if port != activatorPort {
			t.Errorf("%s: port %d, want activator %d", host, port, activatorPort)
		}
	}
	if !strings.Contains(ing.Annotations[activator.HostPortsAnnotation], "admin.example.com=9000") {
		t.Errorf("activator would not know admin's port: %q", ing.Annotations[activator.HostPortsAnnotation])
	}
}
