package controllers

import (
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

const defaultSecret = "api-prod-tls"

func appWithIngress(ing *vestav1alpha1.IngressConfig) *vestav1alpha1.VestaApp {
	return &vestav1alpha1.VestaApp{Spec: vestav1alpha1.VestaAppSpec{Ingress: ing}}
}

func TestResolveCertConfig(t *testing.T) {
	cases := []struct {
		name         string
		app          *vestav1alpha1.IngressConfig
		env          *vestav1alpha1.IngressOverride
		globalIssuer string

		wantIssuer    string
		wantSecret    string
		wantUnmanaged bool
		wantAdopted   bool
	}{
		{
			name:       "nothing configured anywhere",
			wantSecret: defaultSecret,
		},
		{
			name:         "instance default only",
			globalIssuer: "letsencrypt-prod",
			wantIssuer:   "letsencrypt-prod",
			wantSecret:   defaultSecret,
		},
		{
			name:         "app field beats the instance default",
			app:          &vestav1alpha1.IngressConfig{ClusterIssuer: "zerossl"},
			globalIssuer: "letsencrypt-prod",
			wantIssuer:   "zerossl",
			wantSecret:   defaultSecret,
		},
		{
			name:         "env field beats the app field",
			app:          &vestav1alpha1.IngressConfig{ClusterIssuer: "letsencrypt-prod"},
			env:          &vestav1alpha1.IngressOverride{ClusterIssuer: "letsencrypt-staging"},
			globalIssuer: "buypass",
			wantIssuer:   "letsencrypt-staging",
			wantSecret:   defaultSecret,
		},

		// The legacy tier: a hand-written annotation, which before the provider selector
		// existed was the only way to choose an issuer.
		{
			name:         "legacy app annotation beats the instance default",
			app:          &vestav1alpha1.IngressConfig{Annotations: map[string]string{clusterIssuerAnnotation: "custom-ca"}},
			globalIssuer: "letsencrypt-prod",
			wantIssuer:   "custom-ca",
			wantSecret:   defaultSecret,
			wantAdopted:  true,
		},
		{
			name:         "legacy env annotation beats the legacy app annotation",
			app:          &vestav1alpha1.IngressConfig{Annotations: map[string]string{clusterIssuerAnnotation: "custom-ca"}},
			env:          &vestav1alpha1.IngressOverride{Annotations: map[string]string{clusterIssuerAnnotation: "staging-ca"}},
			globalIssuer: "letsencrypt-prod",
			wantIssuer:   "staging-ca",
			wantSecret:   defaultSecret,
			wantAdopted:  true,
		},
		{
			// The one deliberate behaviour change: the annotation used to win because it
			// was merged after the operator's own. An explicit selection now wins.
			name:         "explicit field beats a stale legacy annotation",
			app:          &vestav1alpha1.IngressConfig{ClusterIssuer: "zerossl", Annotations: map[string]string{clusterIssuerAnnotation: "custom-ca"}},
			globalIssuer: "letsencrypt-prod",
			wantIssuer:   "zerossl",
			wantSecret:   defaultSecret,
		},

		// Manual: a certificate the user supplied.
		{
			name:         "app manual secret suppresses the issuer",
			app:          &vestav1alpha1.IngressConfig{TLSSecretName: "wildcard-example-com"},
			globalIssuer: "letsencrypt-prod",
			wantSecret:   "wildcard-example-com",
		},
		{
			name:         "env manual secret beats the app manual secret",
			app:          &vestav1alpha1.IngressConfig{TLSSecretName: "prod-cert"},
			env:          &vestav1alpha1.IngressOverride{TLSSecretName: "staging-cert"},
			globalIssuer: "letsencrypt-prod",
			wantSecret:   "staging-cert",
		},
		{
			name:         "manual mode with no secret yet issues nothing",
			app:          &vestav1alpha1.IngressConfig{TLSMode: tlsModeManual},
			globalIssuer: "letsencrypt-prod",
			wantSecret:   defaultSecret,
		},
		{
			name:         "manual secret beats an explicit issuer field",
			app:          &vestav1alpha1.IngressConfig{ClusterIssuer: "zerossl", TLSSecretName: "byo-cert"},
			globalIssuer: "letsencrypt-prod",
			wantSecret:   "byo-cert",
		},

		// The escape hatch.
		{
			name:          "custom-annotations stamps nothing",
			app:           &vestav1alpha1.IngressConfig{TLSMode: tlsModeCustomAnnotations, ClusterIssuer: "zerossl"},
			globalIssuer:  "letsencrypt-prod",
			wantSecret:    defaultSecret,
			wantUnmanaged: true,
		},
		{
			name:          "env can opt one environment into custom-annotations",
			app:           &vestav1alpha1.IngressConfig{ClusterIssuer: "zerossl"},
			env:           &vestav1alpha1.IngressOverride{TLSMode: tlsModeCustomAnnotations},
			globalIssuer:  "letsencrypt-prod",
			wantSecret:    defaultSecret,
			wantUnmanaged: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveCertConfig(appWithIngress(c.app), c.env, c.globalIssuer, defaultSecret)

			if got.ClusterIssuer != c.wantIssuer {
				t.Errorf("ClusterIssuer = %q, want %q", got.ClusterIssuer, c.wantIssuer)
			}
			if got.SecretName != c.wantSecret {
				t.Errorf("SecretName = %q, want %q", got.SecretName, c.wantSecret)
			}
			if got.Unmanaged != c.wantUnmanaged {
				t.Errorf("Unmanaged = %v, want %v", got.Unmanaged, c.wantUnmanaged)
			}
			if got.AdoptedFromAnnotation != c.wantAdopted {
				t.Errorf("AdoptedFromAnnotation = %v, want %v", got.AdoptedFromAnnotation, c.wantAdopted)
			}
		})
	}
}

// Installs predating the provider selector chose their issuer with a raw annotation,
// because IngressOverride had no issuer field and the annotation merge ran after the
// operator's own stamp. Those apps must keep resolving to the same issuer across the
// upgrade — otherwise a routine operator rollout silently re-issues every certificate
// from whatever the instance default happens to be.
func TestResolveCertConfigPreservesLegacyAnnotationInstalls(t *testing.T) {
	app := appWithIngress(&vestav1alpha1.IngressConfig{
		Domain: "app.example.com",
		TLS:    true,
		// No ClusterIssuer field — there was no UI to set one.
		Annotations: map[string]string{clusterIssuerAnnotation: "internal-ca"},
	})
	env := &vestav1alpha1.IngressOverride{
		Annotations: map[string]string{clusterIssuerAnnotation: "internal-ca-staging"},
	}

	if got := resolveCertConfig(app, nil, "letsencrypt-prod", defaultSecret); got.ClusterIssuer != "internal-ca" {
		t.Errorf("app-level legacy install resolved to %q, want %q", got.ClusterIssuer, "internal-ca")
	}
	if got := resolveCertConfig(app, env, "letsencrypt-prod", defaultSecret); got.ClusterIssuer != "internal-ca-staging" {
		t.Errorf("per-env legacy install resolved to %q, want %q", got.ClusterIssuer, "internal-ca-staging")
	}
}

// An empty annotation value is not a selection. Treating "" as configured would suppress
// the instance default and leave the app with no issuer at all.
func TestResolveCertConfigIgnoresEmptyAnnotation(t *testing.T) {
	app := appWithIngress(&vestav1alpha1.IngressConfig{
		Annotations: map[string]string{clusterIssuerAnnotation: ""},
	})

	got := resolveCertConfig(app, nil, "letsencrypt-prod", defaultSecret)
	if got.ClusterIssuer != "letsencrypt-prod" {
		t.Errorf("ClusterIssuer = %q, want the instance default", got.ClusterIssuer)
	}
	if got.AdoptedFromAnnotation {
		t.Error("AdoptedFromAnnotation = true for an empty annotation")
	}
}
