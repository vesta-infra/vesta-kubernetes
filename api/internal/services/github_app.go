package services

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	GitHubAppSecretName = "vesta-github-app"
	vestaNamespace      = "vesta-system"
	tokenCacheTTL       = 50 * time.Minute

	// maxInstallationPages bounds the paging loop so a paging bug cannot spin forever
	// against GitHub.
	maxInstallationPages = 50 // GitHub installation tokens expire in 60min
)

// GitHubAppService handles GitHub App authentication: JWT generation,
// installation token management, and the manifest creation flow.
type GitHubAppService struct {
	mu            sync.RWMutex
	appID         int64
	privateKey    *rsa.PrivateKey
	webhookSecret string
	configured    bool
	clientset     kubernetes.Interface

	// Installation token cache: installationID -> cachedToken
	tokenCache map[int64]*cachedToken

	// Credentials of Apps other than the original one, keyed by the Secret that holds
	// them. Populated on demand, because a connection's App is only needed when something
	// touches that connection's repositories.
	identities map[string]appIdentity
}

type cachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// GitHubAppCredentials holds the result of the manifest code exchange.
type GitHubAppCredentials struct {
	ID            int64          `json:"id"`
	Slug          string         `json:"slug"`
	Name          string         `json:"name"`
	PEM           string         `json:"pem"`
	WebhookSecret string         `json:"webhook_secret"`
	ClientID      string         `json:"client_id"`
	ClientSecret  string         `json:"client_secret"`
	Owner         GitHubAppOwner `json:"owner"`
}

type GitHubAppOwner struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" or "Organization"
}

// GitHubInstallation represents a GitHub App installation.
type GitHubInstallation struct {
	ID      int64         `json:"id"`
	Account GitHubAccount `json:"account"`
	Repos   []GitHubRepo  `json:"repositories,omitempty"`
}

type GitHubAccount struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Type      string `json:"type"`
}

type GitHubRepo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// NewGitHubAppService creates a new service and tries to load credentials
// from the K8s Secret if it exists.
func NewGitHubAppService(clientset kubernetes.Interface) *GitHubAppService {
	svc := &GitHubAppService{
		clientset:  clientset,
		tokenCache: make(map[int64]*cachedToken),
	}
	svc.loadFromSecret()
	return svc
}

// IsConfigured returns whether the GitHub App is configured.
func (s *GitHubAppService) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configured
}

// AppID returns the configured GitHub App ID.
func (s *GitHubAppService) AppID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appID
}

// WebhookSecret returns the configured webhook secret.
func (s *GitHubAppService) WebhookSecret() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webhookSecret
}

// Configure sets the GitHub App credentials at runtime (hot reload, no restart).
func (s *GitHubAppService) Configure(appID int64, pemKey []byte, webhookSecret string) error {
	pk, err := parsePrivateKey(pemKey)
	if err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.appID = appID
	s.privateKey = pk
	s.webhookSecret = webhookSecret
	s.configured = true
	s.tokenCache = make(map[int64]*cachedToken)
	return nil
}

// Unconfigure removes the GitHub App credentials.
func (s *GitHubAppService) Unconfigure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appID = 0
	s.privateKey = nil
	s.webhookSecret = ""
	s.configured = false
	s.tokenCache = make(map[int64]*cachedToken)
}

// GenerateJWT creates a JWT signed with the App's private key (RS256, 10min expiry).
func (s *GitHubAppService) GenerateJWT() (string, error) {
	s.mu.RLock()
	pk := s.privateKey
	appID := s.appID
	s.mu.RUnlock()

	if pk == nil {
		return "", fmt.Errorf("github app not configured")
	}

	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // clock skew
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", appID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(pk)
}

// GetInstallationToken exchanges a JWT for an installation access token.
// Results are cached for 50 minutes (tokens expire in 60min).
func (s *GitHubAppService) GetInstallationToken(ctx context.Context, installationID int64) (string, error) {
	// Check cache
	s.mu.RLock()
	if ct, ok := s.tokenCache[installationID]; ok && time.Now().Before(ct.ExpiresAt) {
		s.mu.RUnlock()
		return ct.Token, nil
	}
	s.mu.RUnlock()

	jwtToken, err := s.GenerateJWT()
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github installation token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github installation token: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse installation token response: %w", err)
	}

	// Cache the token
	s.mu.Lock()
	s.tokenCache[installationID] = &cachedToken{
		Token:     result.Token,
		ExpiresAt: time.Now().Add(tokenCacheTTL),
	}
	s.mu.Unlock()

	return result.Token, nil
}

// GetInstallationForRepo looks up the installation ID that has access to a repo.
func (s *GitHubAppService) GetInstallationForRepo(ctx context.Context, owner, repo string) (int64, error) {
	jwtToken, err := s.GenerateJWT()
	if err != nil {
		return 0, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/installation", owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github get installation: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("github get installation: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse installation response: %w", err)
	}
	return result.ID, nil
}

// GetTokenForRepo gets an installation token for a specific repo.
// This is the main entry point for other services that need a token.
func (s *GitHubAppService) GetTokenForRepo(ctx context.Context, fullRepo string) (string, error) {
	if !s.IsConfigured() {
		return "", fmt.Errorf("github app not configured")
	}

	owner, repo, err := splitRepo(fullRepo)
	if err != nil {
		return "", err
	}

	installationID, err := s.GetInstallationForRepo(ctx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("find installation for %s: %w", fullRepo, err)
	}

	return s.GetInstallationToken(ctx, installationID)
}

// ListInstallations returns all installations of this GitHub App.
func (s *GitHubAppService) ListInstallations(ctx context.Context) ([]GitHubInstallation, error) {
	jwtToken, err := s.GenerateJWT()
	if err != nil {
		return nil, err
	}

	// Paginated. GitHub returns 30 per page by default and this used to fetch exactly one
	// page, so an App installed in more than 30 places silently reported a truncated list
	// -- and the repositories under the missing installations were invisible with no
	// indication anything was missing.
	var all []GitHubInstallation
	for page := 1; page <= maxInstallationPages; page++ {
		url := fmt.Sprintf("https://api.github.com/app/installations?per_page=100&page=%d", page)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github list installations: %w", err)
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github list installations: HTTP %d: %s", resp.StatusCode, string(body))
		}

		var page_ []GitHubInstallation
		if err := json.Unmarshal(body, &page_); err != nil {
			return nil, err
		}
		all = append(all, page_...)

		// A short page is the last page.
		if len(page_) < 100 {
			break
		}
	}
	return all, nil
}

// ListAccessibleRepos returns all repositories accessible across all installations.
func (s *GitHubAppService) ListAccessibleRepos(ctx context.Context) ([]GitHubRepo, error) {
	installations, err := s.ListInstallations(ctx)
	if err != nil {
		return nil, err
	}

	var allRepos []GitHubRepo
	for _, inst := range installations {
		token, err := s.GetInstallationToken(ctx, inst.ID)
		if err != nil {
			log.Printf("[github-app] skip installation %d: %v", inst.ID, err)
			continue
		}

		page := 1
		for {
			url := fmt.Sprintf("https://api.github.com/installation/repositories?per_page=100&page=%d", page)
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				break
			}
			req.Header.Set("Authorization", "token "+token)
			req.Header.Set("Accept", "application/vnd.github+json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				break
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				break
			}

			var result struct {
				Repositories []GitHubRepo `json:"repositories"`
			}
			if err := json.Unmarshal(body, &result); err != nil || len(result.Repositories) == 0 {
				break
			}
			allRepos = append(allRepos, result.Repositories...)
			if len(result.Repositories) < 100 {
				break
			}
			page++
		}
	}

	return allRepos, nil
}

// ListRepoBranches lists branches for a repository using an installation token.
func (s *GitHubAppService) ListRepoBranches(ctx context.Context, fullRepo string) ([]string, error) {
	token, err := s.GetTokenForRepo(ctx, fullRepo)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/branches?per_page=100", fullRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github list branches: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github list branches: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var branches []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &branches); err != nil {
		return nil, err
	}

	names := make([]string, len(branches))
	for i, b := range branches {
		names[i] = b.Name
	}
	return names, nil
}

// BuildManifest generates the GitHub App Manifest JSON for the manifest creation flow.
func (s *GitHubAppService) BuildManifest(apiBaseURL, appName string) map[string]interface{} {
	return map[string]interface{}{
		"name": appName,
		"url":  apiBaseURL,
		"hook_attributes": map[string]interface{}{
			"url":    apiBaseURL + "/api/v1/webhooks/github",
			"active": true,
		},
		"redirect_url": apiBaseURL + "/api/v1/github/callback",
		"public":       false,
		"default_permissions": map[string]string{
			"contents":    "read",
			"statuses":    "write",
			"deployments": "write",
			"metadata":    "read",
		},
		"default_events": []string{"push"},
	}
}

// ExchangeManifestCode exchanges a temporary code from the manifest flow
// for the full app credentials (App ID, PEM, webhook secret).
func (s *GitHubAppService) ExchangeManifestCode(ctx context.Context, code string) (*GitHubAppCredentials, error) {
	url := fmt.Sprintf("https://api.github.com/app-manifests/%s/conversions", code)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github manifest exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("github manifest exchange: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var creds GitHubAppCredentials
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("parse manifest exchange response: %w", err)
	}
	return &creds, nil
}

// SaveToSecret stores the GitHub App credentials in a K8s Secret.
// SecretNameForConnection is where one connection's App credentials live.
//
// The first App keeps the original hardcoded name so an install that predates connections
// is untouched, and so a rollback still finds its credentials where it expects them.
func SecretNameForConnection(connectionID string, legacy bool) string {
	if legacy || connectionID == "" {
		return GitHubAppSecretName
	}
	return GitHubAppSecretName + "-" + connectionID
}

// SaveToSecretNamed writes credentials to a named Secret.
func (s *GitHubAppService) SaveToSecretNamed(ctx context.Context, name string, creds *GitHubAppCredentials) error {
	return s.saveToSecret(ctx, name, creds)
}

func (s *GitHubAppService) SaveToSecret(ctx context.Context, creds *GitHubAppCredentials) error {
	return s.saveToSecret(ctx, GitHubAppSecretName, creds)
}

func (s *GitHubAppService) saveToSecret(ctx context.Context, secretName string, creds *GitHubAppCredentials) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: vestaNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vesta",
				"app.kubernetes.io/component":  "github-app",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"app-id":         fmt.Sprintf("%d", creds.ID),
			"app-name":       creds.Name,
			"app-slug":       creds.Slug,
			"owner-login":    creds.Owner.Login,
			"owner-type":     creds.Owner.Type,
			"private-key":    creds.PEM,
			"webhook-secret": creds.WebhookSecret,
		},
	}

	_, err := s.clientset.CoreV1().Secrets(vestaNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		// Only an existing Secret justifies an update. Falling through on any error at all
		// hid the real cause -- an RBAC denial on create reported itself as a failed
		// update, against an object carrying no resourceVersion.
		return err
	}

	existing, getErr := s.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	existing.StringData = secret.StringData
	existing.Labels = secret.Labels
	_, err = s.clientset.CoreV1().Secrets(vestaNamespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// DeleteSecret removes the GitHub App K8s Secret.
func (s *GitHubAppService) DeleteSecret(ctx context.Context) error {
	return s.clientset.CoreV1().Secrets(vestaNamespace).Delete(ctx, GitHubAppSecretName, metav1.DeleteOptions{})
}

// loadFromSecret tries to load GitHub App credentials from the K8s Secret on startup.
func (s *GitHubAppService) loadFromSecret() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	secret, err := s.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, GitHubAppSecretName, metav1.GetOptions{})
	if err != nil {
		log.Printf("[github-app] no existing config found (this is normal for first run)")
		return
	}

	appIDStr := string(secret.Data["app-id"])
	pemKey := secret.Data["private-key"]
	webhookSecret := string(secret.Data["webhook-secret"])

	var appID int64
	if _, err := fmt.Sscanf(appIDStr, "%d", &appID); err != nil {
		log.Printf("[github-app] invalid app-id in secret: %v", err)
		return
	}

	if err := s.Configure(appID, pemKey, webhookSecret); err != nil {
		log.Printf("[github-app] failed to configure from secret: %v", err)
		return
	}

	log.Printf("[github-app] loaded config from secret (app ID: %d)", appID)
}

func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8
		k, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("cannot parse private key: PKCS1=%v, PKCS8=%v", err, err2)
		}
		rsaKey, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
		return rsaKey, nil
	}
	return key, nil
}

func splitRepo(fullRepo string) (string, string, error) {
	for i, c := range fullRepo {
		if c == '/' {
			return fullRepo[:i], fullRepo[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid repo format %q, expected owner/repo", fullRepo)
}

// Credentials reports the non-secret facts about the configured App, for the connection
// row that adopts it. The private key and webhook secret are deliberately not included:
// they stay in the Secret.
func (s *GitHubAppService) Credentials() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := map[string]string{}
	if !s.configured {
		return out
	}
	out["appId"] = fmt.Sprintf("%d", s.appID)

	// The display fields live only in the Secret, so read them back rather than holding a
	// second copy in memory that could drift from it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	secret, err := s.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, GitHubAppSecretName, metav1.GetOptions{})
	if err != nil {
		return out
	}
	for _, k := range []string{"app-name", "app-slug", "owner-login", "owner-type"} {
		if v, ok := secret.Data[k]; ok {
			out[secretKeyToCamel(k)] = string(v)
		}
	}
	return out
}

func secretKeyToCamel(k string) string {
	switch k {
	case "app-name":
		return "appName"
	case "app-slug":
		return "appSlug"
	case "owner-login":
		return "ownerLogin"
	case "owner-type":
		return "ownerType"
	}
	return k
}

// --- Per-connection credentials ---
//
// Vesta used to allow exactly one GitHub App, so one set of credentials in memory was
// enough. With several connections each App has its own id, key and webhook secret, held in
// its own Secret, and a token has to be minted with the right one -- minting with the wrong
// App yields a token that is valid but has no access to the repository, which surfaces as a
// 404 from GitHub and reads like a missing repository rather than a wrong credential.

// appIdentity is one App's signing material.
type appIdentity struct {
	appID         int64
	privateKey    *rsa.PrivateKey
	webhookSecret string
}

// identityFor loads an App's credentials from the Secret a connection names.
//
// The original App's Secret short-circuits to the in-memory copy, so the adopted connection
// costs no extra reads and keeps behaving exactly as it did.
func (s *GitHubAppService) identityFor(ctx context.Context, secretName string) (appIdentity, error) {
	if secretName == "" || secretName == GitHubAppSecretName {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if !s.configured {
			return appIdentity{}, fmt.Errorf("github app not configured")
		}
		return appIdentity{appID: s.appID, privateKey: s.privateKey, webhookSecret: s.webhookSecret}, nil
	}

	s.mu.RLock()
	if id, ok := s.identities[secretName]; ok {
		s.mu.RUnlock()
		return id, nil
	}
	s.mu.RUnlock()

	secret, err := s.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return appIdentity{}, fmt.Errorf("read app credentials from %s: %w", secretName, err)
	}

	var appID int64
	if _, err := fmt.Sscanf(string(secret.Data["app-id"]), "%d", &appID); err != nil {
		return appIdentity{}, fmt.Errorf("secret %s has no usable app-id", secretName)
	}
	pk, err := parsePrivateKey(secret.Data["private-key"])
	if err != nil {
		return appIdentity{}, fmt.Errorf("secret %s has no usable private key: %w", secretName, err)
	}

	id := appIdentity{appID: appID, privateKey: pk, webhookSecret: string(secret.Data["webhook-secret"])}

	s.mu.Lock()
	if s.identities == nil {
		s.identities = map[string]appIdentity{}
	}
	s.identities[secretName] = id
	s.mu.Unlock()

	return id, nil
}

// ForgetIdentity drops a cached App, so removing a connection stops its credentials being
// usable without waiting for a restart.
func (s *GitHubAppService) ForgetIdentity(secretName string) {
	s.mu.Lock()
	delete(s.identities, secretName)
	s.mu.Unlock()
}

func (id appIdentity) jwt() (string, error) {
	if id.privateKey == nil {
		return "", fmt.Errorf("github app not configured")
	}
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // clock skew
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", id.appID),
	}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(id.privateKey)
}

// GetTokenForRepoVia mints an installation token using a specific connection's App.
func (s *GitHubAppService) GetTokenForRepoVia(ctx context.Context, secretName, fullRepo string) (string, error) {
	id, err := s.identityFor(ctx, secretName)
	if err != nil {
		return "", err
	}
	jwtToken, err := id.jwt()
	if err != nil {
		return "", err
	}

	owner, repo, err := splitRepo(fullRepo)
	if err != nil {
		return "", err
	}

	installID, err := s.installationForRepo(ctx, jwtToken, owner, repo)
	if err != nil {
		return "", err
	}
	return s.installationToken(ctx, jwtToken, installID)
}

// WebhookSecretVia returns the webhook secret of a specific connection's App.
func (s *GitHubAppService) WebhookSecretVia(ctx context.Context, secretName string) string {
	id, err := s.identityFor(ctx, secretName)
	if err != nil {
		return ""
	}
	return id.webhookSecret
}

// installationForRepo and installationToken are the jwt-taking cores of
// GetInstallationForRepo and GetInstallationToken. They exist separately because those two
// mint a JWT from the one App held in memory, and a per-connection call has already minted
// one from a different App.
func (s *GitHubAppService) installationForRepo(ctx context.Context, jwtToken, owner, repo string) (int64, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/installation", owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github installation lookup: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("github installation lookup: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func (s *GitHubAppService) installationToken(ctx context.Context, jwtToken string, installationID int64) (string, error) {
	s.mu.RLock()
	if ct, ok := s.tokenCache[installationID]; ok && time.Now().Before(ct.ExpiresAt) {
		s.mu.RUnlock()
		return ct.Token, nil
	}
	s.mu.RUnlock()

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github installation token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github installation token: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}

	s.mu.Lock()
	if s.tokenCache == nil {
		s.tokenCache = map[int64]*cachedToken{}
	}
	s.tokenCache[installationID] = &cachedToken{Token: out.Token, ExpiresAt: time.Now().Add(tokenCacheTTL)}
	s.mu.Unlock()

	return out.Token, nil
}
