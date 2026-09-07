package services

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newService() *CertProviderService { return NewCertProviderService(nil, "cert-manager") }

// nested walks a rendered ClusterIssuer, failing the test rather than panicking when a
// path is missing — a wrong shape should read as a clear assertion failure.
func nested(t *testing.T, obj map[string]interface{}, path ...string) interface{} {
	t.Helper()
	v, found, err := unstructured.NestedFieldNoCopy(obj, path...)
	if err != nil || !found {
		t.Fatalf("path %s not found in issuer", strings.Join(path, "."))
	}
	return v
}

func TestBuildClusterIssuerACMEDirectories(t *testing.T) {
	s := newService()

	cases := []struct {
		kind       string
		wantServer string
	}{
		{KindLetsEncrypt, "https://acme-v02.api.letsencrypt.org/directory"},
		{KindLetsEncryptStaging, "https://acme-staging-v02.api.letsencrypt.org/directory"},
		{KindZeroSSL, "https://acme.zerossl.com/v2/DV90"},
		{KindBuypass, "https://api.buypass.com/acme/directory"},
		{KindGoogle, "https://dv.acme-v02.api.pki.goog/directory"},
	}

	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			obj := s.BuildClusterIssuer(ProviderSpec{
				Name: "issuer", Kind: c.kind, Email: "ops@example.com",
				EABKeyID: "kid", EABHMACKey: "hmac",
			})

			if got := nested(t, obj, "spec", "acme", "server"); got != c.wantServer {
				t.Errorf("server = %v, want %v", got, c.wantServer)
			}
			if got := nested(t, obj, "spec", "acme", "email"); got != "ops@example.com" {
				t.Errorf("email = %v", got)
			}
			// The account key is per-issuer; sharing one across issuers would make two
			// providers silently the same ACME account.
			if got := nested(t, obj, "spec", "acme", "privateKeySecretRef", "name"); got != "vesta-acme-issuer" {
				t.Errorf("privateKeySecretRef = %v", got)
			}
		})
	}
}

func TestBuildClusterIssuerCustomACMEUsesSuppliedServer(t *testing.T) {
	obj := newService().BuildClusterIssuer(ProviderSpec{
		Name: "internal", Kind: KindCustomACME,
		Email: "ops@example.com", ACMEServer: "https://acme.internal/directory",
	})

	if got := nested(t, obj, "spec", "acme", "server"); got != "https://acme.internal/directory" {
		t.Errorf("server = %v, want the supplied URL", got)
	}
}

func TestBuildClusterIssuerEAB(t *testing.T) {
	const hmacSentinel = "HMAC-VALUE-THAT-MUST-NOT-APPEAR"

	obj := newService().BuildClusterIssuer(ProviderSpec{
		Name: "zs", Kind: KindZeroSSL, Email: "ops@example.com",
		EABKeyID: "key-id-123", EABHMACKey: hmacSentinel,
	})

	eab := nested(t, obj, "spec", "acme", "externalAccountBinding").(map[string]interface{})
	if eab["keyID"] != "key-id-123" {
		t.Errorf("keyID = %v", eab["keyID"])
	}
	ref := eab["keySecretRef"].(map[string]interface{})
	if ref["name"] != "vesta-ssl-zs" || ref["key"] != "eab-hmac-key" {
		t.Errorf("keySecretRef = %v", ref)
	}

	// The HMAC must be referenced, never inlined — a ClusterIssuer is readable by anyone
	// who can read cluster-scoped resources, which is a far wider set than can read
	// Secrets in cert-manager's namespace.
	if strings.Contains(sprint(obj), hmacSentinel) {
		t.Error("the EAB HMAC key leaked into the ClusterIssuer object")
	}
}

func TestBuildClusterIssuerHTTP01Solver(t *testing.T) {
	obj := newService().BuildClusterIssuer(ProviderSpec{
		Name: "le", Kind: KindLetsEncrypt, Email: "ops@example.com", IngressClass: "traefik",
	})

	solvers := nested(t, obj, "spec", "acme", "solvers").([]interface{})
	solver := solvers[0].(map[string]interface{})
	http01 := solver["http01"].(map[string]interface{})
	if got := http01["ingress"].(map[string]interface{})["class"]; got != "traefik" {
		t.Errorf("ingress class = %v", got)
	}
	if _, ok := solver["dns01"]; ok {
		t.Error("an HTTP-01 provider also declared a DNS-01 solver")
	}
}

func TestBuildClusterIssuerDNS01Solvers(t *testing.T) {
	s := newService()

	cases := []struct {
		provider string
		key      string
		wantKey  string
	}{
		{DNSCloudflare, "cloudflare", "api-token"},
		{DNSDigitalOcean, "digitalocean", "access-token"},
	}

	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			obj := s.BuildClusterIssuer(ProviderSpec{
				Name: "dns", Kind: KindLetsEncrypt, Email: "ops@example.com",
				DNSProvider: c.provider,
			})
			solvers := nested(t, obj, "spec", "acme", "solvers").([]interface{})
			dns01 := solvers[0].(map[string]interface{})["dns01"].(map[string]interface{})
			cfg, ok := dns01[c.key].(map[string]interface{})
			if !ok {
				t.Fatalf("no %s block in dns01: %v", c.key, dns01)
			}
			// Whichever ref key the provider uses, it must point at the Vesta secret.
			found := false
			for _, v := range cfg {
				if ref, ok := v.(map[string]interface{}); ok && ref["name"] == "vesta-ssl-dns" && ref["key"] == c.wantKey {
					found = true
				}
			}
			if !found {
				t.Errorf("secret ref not found or wrong: %v", cfg)
			}
		})
	}
}

func TestBuildClusterIssuerRoute53CarriesNonSecretConfig(t *testing.T) {
	obj := newService().BuildClusterIssuer(ProviderSpec{
		Name: "r53", Kind: KindLetsEncrypt, Email: "ops@example.com",
		DNSProvider: DNSRoute53,
		DNSConfig:   map[string]string{"region": "eu-west-1", "accessKeyId": "AKIA"},
	})

	solvers := nested(t, obj, "spec", "acme", "solvers").([]interface{})
	r53 := solvers[0].(map[string]interface{})["dns01"].(map[string]interface{})["route53"].(map[string]interface{})

	if r53["region"] != "eu-west-1" || r53["accessKeyID"] != "AKIA" {
		t.Errorf("route53 config = %v", r53)
	}
}

func TestBuildClusterIssuerDNSZoneSelector(t *testing.T) {
	obj := newService().BuildClusterIssuer(ProviderSpec{
		Name: "scoped", Kind: KindLetsEncrypt, Email: "ops@example.com",
		DNSProvider: DNSCloudflare, DNSZones: []string{"example.com"},
	})

	solvers := nested(t, obj, "spec", "acme", "solvers").([]interface{})
	sel := solvers[0].(map[string]interface{})["selector"].(map[string]interface{})
	zones := sel["dnsZones"].([]interface{})
	if len(zones) != 1 || zones[0] != "example.com" {
		t.Errorf("dnsZones = %v", zones)
	}
}

func TestBuildClusterIssuerNonACMEKinds(t *testing.T) {
	s := newService()

	selfSigned := s.BuildClusterIssuer(ProviderSpec{Name: "ss", Kind: KindSelfSigned})
	if _, found, _ := unstructured.NestedMap(selfSigned, "spec", "selfSigned"); !found {
		t.Error("selfsigned issuer has no selfSigned block")
	}
	if _, found, _ := unstructured.NestedMap(selfSigned, "spec", "acme"); found {
		t.Error("selfsigned issuer declared an acme block")
	}

	ca := s.BuildClusterIssuer(ProviderSpec{Name: "ca", Kind: KindCA, CASecretName: "corp-ca"})
	if got := nested(t, ca, "spec", "ca", "secretName"); got != "corp-ca" {
		t.Errorf("ca secretName = %v", got)
	}
}

func TestBuildClusterIssuerLabelsMarkVestaOwnership(t *testing.T) {
	obj := newService().BuildClusterIssuer(ProviderSpec{Name: "le", Kind: KindLetsEncrypt, Email: "a@b.c"})

	labels := nested(t, obj, "metadata", "labels").(map[string]interface{})
	if labels[ManagedByLabel] != "vesta" || labels[ComponentLabel] != "cert-provider" {
		t.Errorf("labels = %v", labels)
	}
	// The kind is stored so the UI can render the right form without re-deriving it from
	// the ACME URL.
	if labels["kubernetes.getvesta.sh/provider-kind"] != KindLetsEncrypt {
		t.Errorf("provider-kind label = %v", labels["kubernetes.getvesta.sh/provider-kind"])
	}
}

func TestValidateSpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    ProviderSpec
		wantErr string
	}{
		{
			name: "valid let's encrypt",
			spec: ProviderSpec{Name: "letsencrypt-prod", Kind: KindLetsEncrypt, Email: "ops@example.com"},
		},
		{
			name:    "uppercase name",
			spec:    ProviderSpec{Name: "LetsEncrypt", Kind: KindLetsEncrypt, Email: "ops@example.com"},
			wantErr: "DNS-1123",
		},
		{
			name:    "missing email",
			spec:    ProviderSpec{Name: "le", Kind: KindLetsEncrypt},
			wantErr: "email is required",
		},
		{
			name:    "unknown kind",
			spec:    ProviderSpec{Name: "x", Kind: "wat", Email: "ops@example.com"},
			wantErr: "unknown provider kind",
		},
		{
			name:    "custom acme without a server",
			spec:    ProviderSpec{Name: "x", Kind: KindCustomACME, Email: "ops@example.com"},
			wantErr: "acmeServer is required",
		},
		{
			// ZeroSSL and Google reject registration without EAB, so the form must demand
			// it rather than letting the issuer fail asynchronously.
			name:    "zerossl without EAB",
			spec:    ProviderSpec{Name: "zs", Kind: KindZeroSSL, Email: "ops@example.com"},
			wantErr: "External Account Binding",
		},
		{
			name:    "google without EAB",
			spec:    ProviderSpec{Name: "g", Kind: KindGoogle, Email: "ops@example.com"},
			wantErr: "External Account Binding",
		},
		{
			name: "zerossl with EAB",
			spec: ProviderSpec{Name: "zs", Kind: KindZeroSSL, Email: "ops@example.com", EABKeyID: "k", EABHMACKey: "h"},
		},
		{
			name:    "zerossl with only the key id",
			spec:    ProviderSpec{Name: "zs", Kind: KindZeroSSL, Email: "ops@example.com", EABKeyID: "k"},
			wantErr: "External Account Binding",
		},
		{
			name:    "cloudflare without a token",
			spec:    ProviderSpec{Name: "cf", Kind: KindLetsEncrypt, Email: "ops@example.com", DNSProvider: DNSCloudflare},
			wantErr: "dnsCredentials.apiToken is required",
		},
		{
			name: "cloudflare with a token",
			spec: ProviderSpec{Name: "cf", Kind: KindLetsEncrypt, Email: "ops@example.com",
				DNSProvider: DNSCloudflare, DNSCredentials: map[string]string{"apiToken": "t"}},
		},
		{
			name: "route53 without a region",
			spec: ProviderSpec{Name: "r", Kind: KindLetsEncrypt, Email: "ops@example.com",
				DNSProvider:    DNSRoute53,
				DNSCredentials: map[string]string{"secretAccessKey": "s"},
				DNSConfig:      map[string]string{"accessKeyId": "AKIA"}},
			wantErr: "dnsConfig.region is required",
		},
		{
			name:    "unknown dns provider",
			spec:    ProviderSpec{Name: "x", Kind: KindLetsEncrypt, Email: "ops@example.com", DNSProvider: "azure"},
			wantErr: "unknown DNS provider",
		},
		{
			name: "self-signed needs nothing",
			spec: ProviderSpec{Name: "ss", Kind: KindSelfSigned},
		},
		{
			name:    "ca without a secret",
			spec:    ProviderSpec{Name: "ca", Kind: KindCA},
			wantErr: "caSecretName is required",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSpec(c.spec)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestProviderFromIssuerReadsStatus(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		wantReady  bool
		wantStatus string
	}{
		{"ready", "True", true, "Ready"},
		{"failed", "False", false, "Failed"},
		{"pending", "Unknown", false, "Pending"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := &unstructured.Unstructured{Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "le",
					"labels": map[string]interface{}{
						ManagedByLabel: "vesta", ComponentLabel: "cert-provider",
						"kubernetes.getvesta.sh/provider-kind": KindLetsEncrypt,
					},
				},
				"spec": map[string]interface{}{"acme": map[string]interface{}{"email": "ops@example.com"}},
				"status": map[string]interface{}{"conditions": []interface{}{
					map[string]interface{}{"type": "Ready", "status": c.status, "message": "detail"},
				}},
			}}

			p := ProviderFromIssuer(u, "le")
			if p.Ready != c.wantReady || p.Status != c.wantStatus {
				t.Errorf("Ready=%v Status=%q, want %v/%q", p.Ready, p.Status, c.wantReady, c.wantStatus)
			}
			if p.StatusReason != "detail" {
				t.Errorf("StatusReason = %q", p.StatusReason)
			}
			if !p.IsDefault {
				t.Error("IsDefault = false for the named default issuer")
			}
			if !p.Managed {
				t.Error("Managed = false for a Vesta-labelled issuer")
			}
		})
	}
}

// An issuer created with kubectl has no Vesta labels. It must still be listed and
// classified — hiding it would make a working setup look empty — but never edited.
func TestProviderFromIssuerClassifiesUnmanagedIssuers(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "handmade"},
		"spec": map[string]interface{}{"acme": map[string]interface{}{
			"server": "https://acme-v02.api.letsencrypt.org/directory",
			"email":  "ops@example.com",
			"solvers": []interface{}{
				map[string]interface{}{"http01": map[string]interface{}{
					"ingress": map[string]interface{}{"class": "nginx"},
				}},
			},
		}},
	}}

	p := ProviderFromIssuer(u, "")
	if p.Managed {
		t.Error("Managed = true for an issuer with no Vesta labels")
	}
	// Kind is recovered from the ACME URL so the UI does not show "unknown".
	if p.Kind != KindLetsEncrypt {
		t.Errorf("Kind = %q, want it inferred from the ACME server", p.Kind)
	}
	if p.Solver != "HTTP-01" || p.IngressClass != "nginx" {
		t.Errorf("Solver=%q IngressClass=%q", p.Solver, p.IngressClass)
	}
	if p.Status != "Unknown" {
		t.Errorf("Status = %q, want Unknown with no conditions", p.Status)
	}
}

func TestProviderFromIssuerSummarisesDNS01(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "cf"},
		"spec": map[string]interface{}{"acme": map[string]interface{}{
			"solvers": []interface{}{
				map[string]interface{}{
					"selector": map[string]interface{}{"dnsZones": []interface{}{"example.com"}},
					"dns01":    map[string]interface{}{"cloudflare": map[string]interface{}{}},
				},
			},
		}},
	}}

	p := ProviderFromIssuer(u, "")
	if p.Solver != "DNS-01" || p.DNSProvider != "cloudflare" {
		t.Errorf("Solver=%q DNSProvider=%q", p.Solver, p.DNSProvider)
	}
	if len(p.DNSZones) != 1 || p.DNSZones[0] != "example.com" {
		t.Errorf("DNSZones = %v", p.DNSZones)
	}
}

func sprint(v interface{}) string {
	b := strings.Builder{}
	writeValue(&b, v)
	return b.String()
}

func writeValue(b *strings.Builder, v interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			b.WriteString(k)
			writeValue(b, val)
		}
	case []interface{}:
		for _, val := range t {
			writeValue(b, val)
		}
	case string:
		b.WriteString(t)
	}
}
