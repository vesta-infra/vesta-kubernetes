package controllers

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := vestav1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// Where a middleware gets projected must match where the Ingress naming it is written.
// If the two disagree, the Ingress references a middleware that does not exist -- and
// Traefik drops the entire router for that, so the app returns 404 rather than losing a
// policy. This is the shape of the production failure on utility-prod/vesta-deploy.
func TestNamespacesReferencingMatchesWhereIngressesAreWritten(t *testing.T) {
	scheme := testScheme(t)

	t.Run("an app declaring its environments", func(t *testing.T) {
		app := &vestav1alpha1.VestaApp{
			ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "vesta-system"},
			Spec: vestav1alpha1.VestaAppSpec{
				Project: "retail",
				Environments: []vestav1alpha1.AppEnvironmentConfig{
					{Name: "production"}, {Name: "staging"},
				},
				Ingress: &vestav1alpha1.IngressConfig{Middlewares: []string{"office-only"}},
			},
		}
		r := &VestaMiddlewareReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build(),
			Scheme: scheme,
		}

		got, err := r.namespacesReferencing(context.Background(), "office-only")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"retail-production", "retail-staging"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("an app inheriting its project's environments", func(t *testing.T) {
		// The case the old code got wrong: no spec.environments, so it returned nothing
		// and projected nowhere -- while reconcileIngress still wrote an Ingress into
		// each of the project's namespaces, naming a middleware that was never created.
		app := &vestav1alpha1.VestaApp{
			ObjectMeta: metav1.ObjectMeta{Name: "vesta-deploy", Namespace: "vesta-system"},
			Spec: vestav1alpha1.VestaAppSpec{
				Project: "utility",
				Ingress: &vestav1alpha1.IngressConfig{Middlewares: []string{"office-only"}},
			},
		}
		env := &vestav1alpha1.VestaEnvironment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "prod",
				Namespace: "vesta-system",
				Labels:    map[string]string{"kubernetes.getvesta.sh/project": "utility"},
			},
		}
		r := &VestaMiddlewareReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, env).Build(),
			Scheme: scheme,
		}

		got, err := r.namespacesReferencing(context.Background(), "office-only")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0] != "utility-prod" {
			t.Errorf("got %v, want [utility-prod] -- this is where the Ingress is written", got)
		}
	})

	t.Run("an environment opting out is not projected into", func(t *testing.T) {
		none := []string{}
		app := &vestav1alpha1.VestaApp{
			ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "vesta-system"},
			Spec: vestav1alpha1.VestaAppSpec{
				Project: "retail",
				Environments: []vestav1alpha1.AppEnvironmentConfig{
					{Name: "production"},
					{Name: "staging", Ingress: &vestav1alpha1.IngressOverride{Middlewares: &none}},
				},
				Ingress: &vestav1alpha1.IngressConfig{Middlewares: []string{"office-only"}},
			},
		}
		r := &VestaMiddlewareReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build(),
			Scheme: scheme,
		}

		got, err := r.namespacesReferencing(context.Background(), "office-only")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Projecting into staging anyway would leave an object nothing references, which
		// the GC would then have to distinguish from one still in use.
		if len(got) != 1 || got[0] != "retail-production" {
			t.Errorf("got %v, want [retail-production] only", got)
		}
	})

	t.Run("an unreferenced middleware is projected nowhere", func(t *testing.T) {
		app := &vestav1alpha1.VestaApp{
			ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "vesta-system"},
			Spec: vestav1alpha1.VestaAppSpec{
				Project:      "retail",
				Environments: []vestav1alpha1.AppEnvironmentConfig{{Name: "production"}},
			},
		}
		r := &VestaMiddlewareReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build(),
			Scheme: scheme,
		}

		got, err := r.namespacesReferencing(context.Background(), "office-only")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}
