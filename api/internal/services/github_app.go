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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	githubAppSecretName = "vesta-github-app"
	vestaNamespace      = "vesta-system"
	tokenCacheTTL       = 50 * time.Minute // GitHub installation tokens expire in 60min
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
}

type cachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// GitHubAppCredentials holds the result of the manifest code exchange.
type GitHubAppCredentials struct {
	ID            int64            `json:"id"`
	Slug          string           `json:"slug"`
	Name          string           `json:"name"`
	PEM           string           `json:"pem"`
	WebhookSecret string           `json:"webhook_secret"`
	ClientID      string           `json:"client_id"`
	ClientSecret  string           `json:"client_secret"`
	Owner         GitHubAppOwner   `json:"owner"`
}

type GitHubAppOwner struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" or "Organization"
}

// GitHubInstallation represents a GitHub App installation.
type GitHubInstallation struct {
	ID      int64                `json:"id"`
	Account GitHubAccount        `json:"account"`
	Repos   []GitHubRepo         `json:"repositories,omitempty"`
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

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/app/installations", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github list installations: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github list installations: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var installations []GitHubInstallation
	if err := json.Unmarshal(body, &installations); err != nil {
		return nil, err
	}
	return installations, nil
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
func (s *GitHubAppService) SaveToSecret(ctx context.Context, creds *GitHubAppCredentials) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      githubAppSecretName,
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

	// Try to create; if exists, update
	_, err := s.clientset.CoreV1().Secrets(vestaNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		_, err = s.clientset.CoreV1().Secrets(vestaNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	}
	return err
}

// DeleteSecret removes the GitHub App K8s Secret.
func (s *GitHubAppService) DeleteSecret(ctx context.Context) error {
	return s.clientset.CoreV1().Secrets(vestaNamespace).Delete(ctx, githubAppSecretName, metav1.DeleteOptions{})
}

// loadFromSecret tries to load GitHub App credentials from the K8s Secret on startup.
func (s *GitHubAppService) loadFromSecret() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	secret, err := s.clientset.CoreV1().Secrets(vestaNamespace).Get(ctx, githubAppSecretName, metav1.GetOptions{})
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
