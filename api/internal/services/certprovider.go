// Package services — certprovider builds and inspects the cert-manager ClusterIssuers
// behind Settings -> SSL Certificates.
//
// Vesta creates issuers rather than only naming them, because before this existed a user
// had to hand-write a ClusterIssuer with kubectl and happen to give it the same name the
// Helm default expected, or TLS silently never provisioned.
package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
)

const (
	// ManagedByLabel and ComponentLabel mark the issuers Vesta itself created. Issuers
	// without them were made out-of-band; they stay listable and selectable but are never
	// edited or deleted from the UI.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ComponentLabel = "kubernetes.getvesta.sh/component"

	managedByValue     = "vesta"
	certProviderValue  = "cert-provider"
	providerKindLabel  = "kubernetes.getvesta.sh/provider-kind"
	defaultCMNamespace = "cert-manager"
)

// ClusterIssuerCRD is the CRD whose presence means cert-manager is installed.
const ClusterIssuerCRD = "clusterissuers.cert-manager.io"

// Provider kinds. Each maps to an ACME directory URL or a non-ACME issuer shape.
const (
	KindLetsEncrypt        = "letsencrypt"
	KindLetsEncryptStaging = "letsencrypt-staging"
	KindZeroSSL            = "zerossl"
	KindBuypass            = "buypass"
	KindGoogle             = "google"
	KindCustomACME         = "custom-acme"
	KindSelfSigned         = "selfsigned"
	KindCA                 = "ca"
)

// acmeDirectory maps a provider kind to its ACME directory URL, and records whether the
// CA requires External Account Binding. ZeroSSL and Google Trust Services reject account
// registration outright without EAB credentials, so the form must demand them up front
// rather than letting the issuer fail asynchronously with an opaque status.
var acmeDirectory = map[string]struct {
	URL         string
	EABRequired bool
}{
	KindLetsEncrypt:        {URL: "https://acme-v02.api.letsencrypt.org/directory"},
	KindLetsEncryptStaging: {URL: "https://acme-staging-v02.api.letsencrypt.org/directory"},
	KindZeroSSL:            {URL: "https://acme.zerossl.com/v2/DV90", EABRequired: true},
	KindBuypass:            {URL: "https://api.buypass.com/acme/directory"},
	KindGoogle:             {URL: "https://dv.acme-v02.api.pki.goog/directory", EABRequired: true},
	KindCustomACME:         {}, // URL supplied by the user
}

// DNS-01 provider kinds.
const (
	DNSCloudflare   = "cloudflare"
	DNSRoute53      = "route53"
	DNSDigitalOcean = "digitalocean"
	DNSCloudDNS     = "clouddns"
)

// ProviderSpec is the API-facing description of an SSL provider, before it becomes a
// ClusterIssuer. Credentials are write-only: they are accepted here and never returned.
type ProviderSpec struct {
	Name string `json:"name"`
	Kind string `json:"kind"`

	// Email is the ACME account contact. Required for every ACME kind.
	Email string `json:"email,omitempty"`
	// ACMEServer overrides the directory URL. Required for custom-acme.
	ACMEServer string `json:"acmeServer,omitempty"`

	// External Account Binding, required by ZeroSSL and Google Trust Services.
	EABKeyID   string `json:"eabKeyId,omitempty"`
	EABHMACKey string `json:"eabHmacKey,omitempty"`

	// Solver: exactly one of these. Empty DNSProvider means HTTP-01.
	IngressClass string `json:"ingressClass,omitempty"`
	DNSProvider  string `json:"dnsProvider,omitempty"`
	// DNSConfig carries the provider-specific non-secret fields (region, project, ...).
	DNSConfig map[string]string `json:"dnsConfig,omitempty"`
	// DNSCredentials carries the secret fields. Written to a Secret, never read back.
	DNSCredentials map[string]string `json:"dnsCredentials,omitempty"`
	// DNSZones optionally scopes the solver to specific zones.
	DNSZones []string `json:"dnsZones,omitempty"`

	// CASecretName is the existing Secret holding a private CA keypair, for kind "ca".
	CASecretName string `json:"caSecretName,omitempty"`
}

// Provider is what the API returns: everything above minus the credentials, plus the
// issuer's live readiness.
type Provider struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Email        string   `json:"email,omitempty"`
	ACMEServer   string   `json:"acmeServer,omitempty"`
	Solver       string   `json:"solver,omitempty"`
	DNSProvider  string   `json:"dnsProvider,omitempty"`
	IngressClass string   `json:"ingressClass,omitempty"`
	DNSZones     []string `json:"dnsZones,omitempty"`

	Ready        bool   `json:"ready"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason,omitempty"`

	// Managed is false for issuers created outside Vesta. The UI offers those for
	// selection but not for editing — Vesta does not own their shape.
	Managed   bool `json:"managed"`
	IsDefault bool `json:"isDefault"`
}

// CertProviderService creates and inspects cert-manager ClusterIssuers.
type CertProviderService struct {
	clientset kubernetes.Interface
	// namespace is cert-manager's own controller namespace. ClusterIssuer secretRefs
	// resolve relative to it, NOT to vesta-system — a credential Secret written anywhere
	// else is simply not found, and the issuer sits Pending with an obscure message.
	namespace string
}

func NewCertProviderService(clientset kubernetes.Interface, namespace string) *CertProviderService {
	if namespace == "" {
		namespace = defaultCMNamespace
	}
	return &CertProviderService{clientset: clientset, namespace: namespace}
}

// Namespace is cert-manager's controller namespace, where credential Secrets must live.
func (s *CertProviderService) Namespace() string { return s.namespace }

// EABRequired reports whether a provider kind cannot register without External Account
// Binding credentials.
func EABRequired(kind string) bool { return acmeDirectory[kind].EABRequired }

// IsACME reports whether a kind speaks ACME (and therefore needs an email and a solver).
func IsACME(kind string) bool {
	_, ok := acmeDirectory[kind]
	return ok
}

// ValidateSpec checks a provider before anything is written, so a bad request fails as a
// 400 with a named field rather than as a ClusterIssuer that never becomes ready.
func ValidateSpec(spec ProviderSpec) error {
	if !isDNSName(spec.Name) {
		return fmt.Errorf("name must be a lowercase DNS-1123 name (letters, digits, '-')")
	}

	switch spec.Kind {
	case KindSelfSigned:
		return nil
	case KindCA:
		if spec.CASecretName == "" {
			return fmt.Errorf("caSecretName is required for a private CA issuer")
		}
		return nil
	}

	if !IsACME(spec.Kind) {
		return fmt.Errorf("unknown provider kind %q", spec.Kind)
	}
	if spec.Email == "" {
		return fmt.Errorf("email is required: the CA sends expiry warnings to it")
	}
	if spec.Kind == KindCustomACME && spec.ACMEServer == "" {
		return fmt.Errorf("acmeServer is required for a custom ACME provider")
	}
	if EABRequired(spec.Kind) && (spec.EABKeyID == "" || spec.EABHMACKey == "") {
		return fmt.Errorf("%s requires External Account Binding credentials (key ID and HMAC key)", spec.Kind)
	}
	if spec.DNSProvider != "" {
		if err := validateDNSProvider(spec); err != nil {
			return err
		}
	}
	return nil
}

func validateDNSProvider(spec ProviderSpec) error {
	required := map[string][]string{
		DNSCloudflare:   {"apiToken"},
		DNSRoute53:      {"secretAccessKey"},
		DNSDigitalOcean: {"token"},
		DNSCloudDNS:     {"serviceAccountJson"},
	}
	keys, ok := required[spec.DNSProvider]
	if !ok {
		return fmt.Errorf("unknown DNS provider %q", spec.DNSProvider)
	}
	for _, k := range keys {
		if strings.TrimSpace(spec.DNSCredentials[k]) == "" {
			return fmt.Errorf("dnsCredentials.%s is required for %s", k, spec.DNSProvider)
		}
	}
	if spec.DNSProvider == DNSRoute53 {
		if spec.DNSConfig["accessKeyId"] == "" {
			return fmt.Errorf("dnsConfig.accessKeyId is required for route53")
		}
		if spec.DNSConfig["region"] == "" {
			return fmt.Errorf("dnsConfig.region is required for route53")
		}
	}
	if spec.DNSProvider == DNSCloudDNS && spec.DNSConfig["project"] == "" {
		return fmt.Errorf("dnsConfig.project is required for clouddns")
	}
	return nil
}

// BuildClusterIssuer renders the ClusterIssuer object for a provider. Secret references
// point at names this service writes via SaveCredentials.
func (s *CertProviderService) BuildClusterIssuer(spec ProviderSpec) map[string]interface{} {
	issuerSpec := map[string]interface{}{}

	switch spec.Kind {
	case KindSelfSigned:
		issuerSpec["selfSigned"] = map[string]interface{}{}
	case KindCA:
		issuerSpec["ca"] = map[string]interface{}{"secretName": spec.CASecretName}
	default:
		issuerSpec["acme"] = s.buildACME(spec)
	}

	return map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata": map[string]interface{}{
			"name": spec.Name,
			"labels": map[string]interface{}{
				ManagedByLabel:    managedByValue,
				ComponentLabel:    certProviderValue,
				providerKindLabel: spec.Kind,
			},
		},
		"spec": issuerSpec,
	}
}

func (s *CertProviderService) buildACME(spec ProviderSpec) map[string]interface{} {
	server := spec.ACMEServer
	if server == "" {
		server = acmeDirectory[spec.Kind].URL
	}

	acme := map[string]interface{}{
		"server": server,
		"email":  spec.Email,
		// cert-manager writes the generated ACME account key here. It is per-issuer, so
		// the name is derived from the issuer name rather than shared.
		"privateKeySecretRef": map[string]interface{}{
			"name": AccountKeySecretName(spec.Name),
		},
		"solvers": []interface{}{s.buildSolver(spec)},
	}

	if spec.EABKeyID != "" {
		acme["externalAccountBinding"] = map[string]interface{}{
			"keyID": spec.EABKeyID,
			"keySecretRef": map[string]interface{}{
				"name": CredentialSecretName(spec.Name),
				"key":  "eab-hmac-key",
			},
		}
	}

	return acme
}

func (s *CertProviderService) buildSolver(spec ProviderSpec) map[string]interface{} {
	solver := map[string]interface{}{}

	// Scope the solver to specific zones when asked, so one issuer can serve a delegated
	// subdomain without claiming the whole account's DNS.
	if len(spec.DNSZones) > 0 {
		zones := make([]interface{}, 0, len(spec.DNSZones))
		for _, z := range spec.DNSZones {
			zones = append(zones, z)
		}
		solver["selector"] = map[string]interface{}{"dnsZones": zones}
	}

	if spec.DNSProvider == "" {
		ingress := map[string]interface{}{}
		if spec.IngressClass != "" {
			ingress["class"] = spec.IngressClass
		}
		solver["http01"] = map[string]interface{}{"ingress": ingress}
		return solver
	}

	secretName := CredentialSecretName(spec.Name)
	dns01 := map[string]interface{}{}

	switch spec.DNSProvider {
	case DNSCloudflare:
		dns01["cloudflare"] = map[string]interface{}{
			"apiTokenSecretRef": map[string]interface{}{"name": secretName, "key": "api-token"},
		}
	case DNSRoute53:
		dns01["route53"] = map[string]interface{}{
			"region":      spec.DNSConfig["region"],
			"accessKeyID": spec.DNSConfig["accessKeyId"],
			"secretAccessKeySecretRef": map[string]interface{}{
				"name": secretName, "key": "secret-access-key",
			},
		}
	case DNSDigitalOcean:
		dns01["digitalocean"] = map[string]interface{}{
			"tokenSecretRef": map[string]interface{}{"name": secretName, "key": "access-token"},
		}
	case DNSCloudDNS:
		dns01["cloudDNS"] = map[string]interface{}{
			"project": spec.DNSConfig["project"],
			"serviceAccountSecretRef": map[string]interface{}{
				"name": secretName, "key": "service-account.json",
			},
		}
	}

	solver["dns01"] = dns01
	return solver
}

// AccountKeySecretName is where cert-manager stores the generated ACME account key.
func AccountKeySecretName(provider string) string { return "vesta-acme-" + provider }

// CredentialSecretName is where Vesta stores the credentials the user supplied.
func CredentialSecretName(provider string) string { return "vesta-ssl-" + provider }

// credentialKeys maps a DNS provider's request field to the Secret key the ClusterIssuer
// references.
var credentialKeys = map[string]map[string]string{
	DNSCloudflare:   {"apiToken": "api-token"},
	DNSRoute53:      {"secretAccessKey": "secret-access-key"},
	DNSDigitalOcean: {"token": "access-token"},
	DNSCloudDNS:     {"serviceAccountJson": "service-account.json"},
}

// SaveCredentials writes the provider's secret material into cert-manager's namespace.
// Called before the ClusterIssuer is created so the issuer never briefly references a
// Secret that does not exist.
func (s *CertProviderService) SaveCredentials(ctx context.Context, spec ProviderSpec) error {
	data := map[string]string{}

	if spec.EABHMACKey != "" {
		data["eab-hmac-key"] = spec.EABHMACKey
	}
	for field, value := range spec.DNSCredentials {
		if key, ok := credentialKeys[spec.DNSProvider][field]; ok && value != "" {
			data[key] = value
		}
	}

	if len(data) == 0 {
		return nil
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CredentialSecretName(spec.Name),
			Namespace: s.namespace,
			Labels: map[string]string{
				ManagedByLabel:    managedByValue,
				ComponentLabel:    certProviderValue,
				providerKindLabel: spec.Kind,
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}

	existing, err := s.clientset.CoreV1().Secrets(s.namespace).Get(ctx, secret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = s.clientset.CoreV1().Secrets(s.namespace).Create(ctx, secret, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}

	// Merge rather than replace: an update that only changes the ingress class must not
	// wipe a credential the form did not resubmit (it cannot — secrets are never read back).
	merged := existing.DeepCopy()
	merged.Labels = secret.Labels
	merged.StringData = data
	_, err = s.clientset.CoreV1().Secrets(s.namespace).Update(ctx, merged, metav1.UpdateOptions{})
	return err
}

// DeleteCredentials removes a provider's secret material. The generated ACME account key
// is deleted too — keeping it would leave an orphan that a same-named issuer would silently
// adopt later, reusing an account the user believed they had removed.
func (s *CertProviderService) DeleteCredentials(ctx context.Context, name string) error {
	var errs []string
	for _, secretName := range []string{CredentialSecretName(name), AccountKeySecretName(name)} {
		err := s.clientset.CoreV1().Secrets(s.namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Sprintf("%s: %v", secretName, err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ProviderFromIssuer projects a ClusterIssuer back into the API shape, reading readiness
// from its status conditions. Credentials are not represented — they are never read back.
func ProviderFromIssuer(u *unstructured.Unstructured, defaultIssuer string) Provider {
	labels := u.GetLabels()

	p := Provider{
		Name:      u.GetName(),
		Kind:      labels[providerKindLabel],
		Managed:   labels[ManagedByLabel] == managedByValue && labels[ComponentLabel] == certProviderValue,
		IsDefault: u.GetName() == defaultIssuer,
		Status:    "Unknown",
	}

	spec, _, _ := unstructured.NestedMap(u.Object, "spec")

	if acme, ok := spec["acme"].(map[string]interface{}); ok {
		p.Email, _ = acme["email"].(string)
		p.ACMEServer, _ = acme["server"].(string)
		if p.Kind == "" {
			p.Kind = kindFromServer(p.ACMEServer)
		}
		p.Solver, p.DNSProvider, p.IngressClass, p.DNSZones = solverSummary(acme)
	} else if _, ok := spec["selfSigned"]; ok {
		if p.Kind == "" {
			p.Kind = KindSelfSigned
		}
	} else if _, ok := spec["ca"]; ok {
		if p.Kind == "" {
			p.Kind = KindCA
		}
	}

	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != "Ready" {
			continue
		}
		status, _ := cond["status"].(string)
		p.Ready = status == "True"
		p.StatusReason, _ = cond["message"].(string)
		switch status {
		case "True":
			p.Status = "Ready"
		case "False":
			p.Status = "Failed"
		default:
			p.Status = "Pending"
		}
		break
	}

	return p
}

// kindFromServer labels issuers Vesta did not create, so a hand-made Let's Encrypt issuer
// is not shown as an unhelpful "unknown".
func kindFromServer(server string) string {
	switch {
	case strings.Contains(server, "acme-staging-v02.api.letsencrypt.org"):
		return KindLetsEncryptStaging
	case strings.Contains(server, "acme-v02.api.letsencrypt.org"):
		return KindLetsEncrypt
	case strings.Contains(server, "acme.zerossl.com"):
		return KindZeroSSL
	case strings.Contains(server, "api.buypass.com"):
		return KindBuypass
	case strings.Contains(server, "pki.goog"):
		return KindGoogle
	case server != "":
		return KindCustomACME
	}
	return ""
}

func solverSummary(acme map[string]interface{}) (solver, dnsProvider, ingressClass string, zones []string) {
	solvers, _ := acme["solvers"].([]interface{})
	if len(solvers) == 0 {
		return "", "", "", nil
	}
	first, ok := solvers[0].(map[string]interface{})
	if !ok {
		return "", "", "", nil
	}

	if sel, ok := first["selector"].(map[string]interface{}); ok {
		if dz, ok := sel["dnsZones"].([]interface{}); ok {
			for _, z := range dz {
				if s, ok := z.(string); ok {
					zones = append(zones, s)
				}
			}
		}
	}

	if http01, ok := first["http01"].(map[string]interface{}); ok {
		if ing, ok := http01["ingress"].(map[string]interface{}); ok {
			ingressClass, _ = ing["class"].(string)
		}
		return "HTTP-01", "", ingressClass, zones
	}
	if dns01, ok := first["dns01"].(map[string]interface{}); ok {
		for k := range dns01 {
			dnsProvider = k
			break
		}
		return "DNS-01", dnsProvider, "", zones
	}
	return "", "", "", zones
}

// isDNSName reports whether s is a valid lowercase DNS-1123 subdomain label sequence,
// which is what Kubernetes requires of a ClusterIssuer name.
func isDNSName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' || r == '.':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
