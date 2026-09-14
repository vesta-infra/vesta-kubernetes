package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	mathrand "math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/git"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// manifestStateTTL bounds how long a half-finished App registration stays valid. Long
// enough to fill in GitHub's form, short enough that an abandoned attempt does not linger.
const manifestStateTTL = 30 * time.Minute

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GetGitHubAppManifest returns the manifest JSON and the GitHub URL to POST it to.
// POST /api/v1/github/manifest
func (h *Handler) GetGitHubAppManifest(c *gin.Context) {
	if h.GitHubApp == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "github app service not available"})
		return
	}

	var req struct {
		Organization string `json:"organization"`
		AppName      string `json:"appName"`
		APIBaseURL   string `json:"apiBaseUrl"`
		UIBaseURL    string `json:"uiBaseUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request"})
		return
	}

	if req.AppName == "" {
		req.AppName = fmt.Sprintf("vesta-%04d", mathrand.Intn(10000))
	}
	if req.APIBaseURL == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "apiBaseUrl is required"})
		return
	}

	state, err := generateState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate state"})
		return
	}

	// The connection's id has to exist before the manifest does: GitHub records the webhook
	// URL when the App is created, and that URL carries the id so deliveries can be matched
	// to the right App later. Changing it afterwards means editing the App on GitHub, so it
	// is decided here and honoured by the callback.
	connectionID := uuid.NewString()

	// The first App keeps the original Secret name and the connection-less webhook URL, so
	// an install that has never had one behaves exactly as before. Only a second App needs
	// a scoped URL.
	first := !h.GitHubApp.IsConfigured()
	webhookPath := "/api/v1/webhooks/github"
	if !first {
		webhookPath = "/api/v1/webhooks/github/" + connectionID
	}

	// Stored in Postgres, not in this process.
	//
	// It used to be a package-level map guarded by a mutex, which had two problems: it
	// never expired, so every abandoned registration leaked for the life of the process,
	// and with more than one API replica the callback could land on a pod that had never
	// seen the state and answered 403 to a perfectly valid return from GitHub.
	if err := h.DB.PutOAuthState(c.Request.Context(), state, git.ProviderGitHub,
		map[string]string{
			"uiBaseUrl":    req.UIBaseURL,
			"connectionId": connectionID,
			"first":        fmt.Sprintf("%t", first),
		}, manifestStateTTL); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to record state"})
		return
	}

	manifest := h.GitHubApp.BuildManifest(req.APIBaseURL, req.AppName)
	// Override the hook URL the manifest defaults to, so a second App's deliveries name
	// their own connection.
	if hook, ok := manifest["hook_attributes"].(map[string]interface{}); ok {
		hook["url"] = strings.TrimSuffix(req.APIBaseURL, "/") + webhookPath
	}

	// Build the GitHub URL
	githubURL := "https://github.com/settings/apps/new"
	if req.Organization != "" {
		githubURL = fmt.Sprintf("https://github.com/organizations/%s/settings/apps/new", req.Organization)
	}

	c.JSON(http.StatusOK, gin.H{
		"manifest":  manifest,
		"githubUrl": githubURL,
		"state":     state,
	})
}

// GitHubAppCallback handles the redirect from GitHub after the manifest flow.
// GET /api/v1/github/callback?code=xxx&state=yyy
func (h *Handler) GitHubAppCallback(c *gin.Context) {
	if h.GitHubApp == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "github app service not available"})
		return
	}

	code := c.Query("code")
	state := c.Query("state")

	if code == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "missing code parameter"})
		return
	}

	// Verify state to prevent CSRF. The read consumes it, so a state cannot be replayed.
	_, draft, err := h.DB.TakeOAuthState(c.Request.Context(), state)
	if err != nil {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: "invalid or expired state parameter"})
		return
	}
	uiBaseURL := draft["uiBaseUrl"]

	// Exchange the code for credentials
	creds, err := h.GitHubApp.ExchangeManifestCode(c.Request.Context(), code)
	if err != nil {
		log.Printf("[github-app] manifest code exchange failed: %v", err)
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: "failed to exchange code with GitHub"})
		return
	}

	connectionID := draft["connectionId"]
	first := draft["first"] != "false"
	secretName := services.SecretNameForConnection(connectionID, first)

	if err := h.GitHubApp.SaveToSecretNamed(c.Request.Context(), secretName, creds); err != nil {
		log.Printf("[github-app] failed to save credentials: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to save credentials"})
		return
	}

	// Only the first App becomes the in-memory one. A second must not overwrite it, or the
	// original connection would start minting tokens with the wrong App's key.
	if first {
		if err := h.GitHubApp.Configure(creds.ID, []byte(creds.PEM), creds.WebhookSecret); err != nil {
			log.Printf("[github-app] failed to configure service: %v", err)
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to configure github app"})
			return
		}
	}

	if _, err := h.DB.CreateGitConnection(c.Request.Context(), db.GitConnection{
		ID:          connectionID,
		Provider:    git.ProviderGitHub,
		DisplayName: creds.Name,
		Host:        git.DefaultHost(git.ProviderGitHub),
		Account:     creds.Owner.Login,
		SecretName:  secretName,
		ExternalID:  fmt.Sprintf("%d", creds.ID),
		Metadata:    map[string]string{"appSlug": creds.Slug, "ownerType": creds.Owner.Type},
		IsLegacy:    first,
	}); err != nil {
		// The App exists on GitHub at this point, so failing the request would strand it.
		// Record the problem and let the user retry from Settings instead.
		log.Printf("[github-app] app created but the connection was not recorded: %v", err)
	}

	log.Printf("[github-app] successfully created GitHub App: %s (ID: %d)", creds.Name, creds.ID)

	// Redirect back to the UI settings page
	if uiBaseURL != "" {
		c.Redirect(http.StatusFound, uiBaseURL+"/settings?tab=integrations&github=success")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "configured",
		"appId":  creds.ID,
		"name":   creds.Name,
		"slug":   creds.Slug,
	})
}

// GetGitHubAppStatus returns the current GitHub App configuration status.
// GET /api/v1/settings/github-app
func (h *Handler) GetGitHubAppStatus(c *gin.Context) {
	if h.GitHubApp == nil || !h.GitHubApp.IsConfigured() {
		c.JSON(http.StatusOK, gin.H{"configured": false})
		return
	}

	// Get app info from the K8s secret (for the name)
	secret, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).Get(
		c.Request.Context(), "vesta-github-app", metav1.GetOptions{})

	appName := ""
	appSlug := ""
	ownerLogin := ""
	ownerType := ""
	if err == nil {
		appName = string(secret.Data["app-name"])
		appSlug = string(secret.Data["app-slug"])
		ownerLogin = string(secret.Data["owner-login"])
		ownerType = string(secret.Data["owner-type"])
	}

	// List installations to show count
	installations, _ := h.GitHubApp.ListInstallations(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"configured":    true,
		"appId":         h.GitHubApp.AppID(),
		"appName":       appName,
		"appSlug":       appSlug,
		"ownerLogin":    ownerLogin,
		"ownerType":     ownerType,
		"installations": len(installations),
	})
}

// ListGitHubAppInstallations returns all installations and their repos.
// GET /api/v1/settings/github-app/installations
func (h *Handler) ListGitHubAppInstallations(c *gin.Context) {
	if h.GitHubApp == nil || !h.GitHubApp.IsConfigured() {
		c.JSON(http.StatusOK, gin.H{"installations": []interface{}{}})
		return
	}

	installations, err := h.GitHubApp.ListInstallations(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: "failed to list installations: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"installations": installations})
}

// DeleteGitHubApp removes the GitHub App configuration.
// DELETE /api/v1/settings/github-app
func (h *Handler) DeleteGitHubApp(c *gin.Context) {
	if h.GitHubApp == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "github app service not available"})
		return
	}

	if err := h.GitHubApp.DeleteSecret(c.Request.Context()); err != nil {
		log.Printf("[github-app] failed to delete secret: %v", err)
	}

	h.GitHubApp.Unconfigure()

	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

// ListRepoBranches lists branches for a repository via the GitHub App.
// GET /api/v1/git/branches?repo=org/repo
