package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/models"
)

// In-memory state store for manifest flow CSRF protection.
// In production, this could be stored in DB or a short-lived K8s ConfigMap.
var manifestStates = struct {
	sync.Mutex
	m map[string]bool
}{m: make(map[string]bool)}

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

	if h.GitHubApp.IsConfigured() {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "github app already configured"})
		return
	}

	var req struct {
		Organization string `json:"organization"`
		AppName      string `json:"appName"`
		APIBaseURL   string `json:"apiBaseUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request"})
		return
	}

	if req.AppName == "" {
		req.AppName = "Vesta"
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

	manifestStates.Lock()
	manifestStates.m[state] = true
	manifestStates.Unlock()

	manifest := h.GitHubApp.BuildManifest(req.APIBaseURL, req.AppName)

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

	// Verify state to prevent CSRF
	manifestStates.Lock()
	valid := manifestStates.m[state]
	if valid {
		delete(manifestStates.m, state)
	}
	manifestStates.Unlock()

	if !valid {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: "invalid or expired state parameter"})
		return
	}

	// Exchange the code for credentials
	creds, err := h.GitHubApp.ExchangeManifestCode(c.Request.Context(), code)
	if err != nil {
		log.Printf("[github-app] manifest code exchange failed: %v", err)
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: "failed to exchange code with GitHub"})
		return
	}

	// Save credentials to K8s Secret
	if err := h.GitHubApp.SaveToSecret(c.Request.Context(), creds.ID, creds.Name, creds.PEM, creds.WebhookSecret); err != nil {
		log.Printf("[github-app] failed to save credentials: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to save credentials"})
		return
	}

	// Hot-reload the service
	if err := h.GitHubApp.Configure(creds.ID, []byte(creds.PEM), creds.WebhookSecret); err != nil {
		log.Printf("[github-app] failed to configure service: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to configure github app"})
		return
	}

	log.Printf("[github-app] successfully created GitHub App: %s (ID: %d)", creds.Name, creds.ID)

	// Check for a UI redirect — if the request came from a browser form
	uiURL := c.Query("ui_url")
	if uiURL != "" {
		c.Redirect(http.StatusFound, uiURL+"?tab=integrations&github=success")
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
	if err == nil {
		appName = string(secret.Data["app-name"])
	}

	// List installations to show count
	installations, _ := h.GitHubApp.ListInstallations(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"configured":    true,
		"appId":         h.GitHubApp.AppID(),
		"appName":       appName,
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
