package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/git"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// AdoptLegacyGitHubApp records the pre-connections GitHub App as a connection row.
//
// Adoption rather than migration: the Secret is left exactly where it is and the row simply
// points at it. Nothing is copied, nothing is rewritten, and rolling back to the previous
// release leaves a working install -- the old code reads the Secret by its hardcoded name
// and never looks at this table.
//
// It also gives webhooks created before this release somewhere to resolve to. Those point
// at /api/v1/webhooks/github with no connection in the URL, and that path finds this row.
func AdoptLegacyGitHubApp(ctx context.Context, database *db.DB, app *services.GitHubAppService) {
	if database == nil || app == nil || !app.IsConfigured() {
		return
	}

	if _, err := database.GetLegacyGitConnection(ctx, git.ProviderGitHub); err == nil {
		return // already adopted
	} else if !errors.Is(err, db.ErrNotFound) {
		log.Printf("[git] could not check for an adopted GitHub App: %v", err)
		return
	}

	meta := app.Credentials()
	conn := db.GitConnection{
		Provider:    git.ProviderGitHub,
		DisplayName: firstNonEmpty(meta["appName"], "GitHub"),
		Host:        git.DefaultHost(git.ProviderGitHub),
		Account:     meta["ownerLogin"],
		SecretName:  services.GitHubAppSecretName,
		ExternalID:  meta["appId"],
		Metadata:    map[string]string{"appSlug": meta["appSlug"], "ownerType": meta["ownerType"]},
		IsLegacy:    true,
	}

	if _, err := database.CreateGitConnection(ctx, conn); err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			return
		}
		log.Printf("[git] could not adopt the existing GitHub App as a connection: %v", err)
		return
	}
	log.Printf("[git] adopted the existing GitHub App as a connection")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// connectionFor resolves which connection serves a repository.
//
// An app written before connections existed carries only a provider and a repository, so
// the lookup is by provider and host with the owner used to disambiguate. spec.git.connectionId
// short-circuits it when the repository was chosen through the UI.
func (h *Handler) connectionFor(ctx context.Context, ref git.RepoRef, connectionID string) git.Connection {
	if connectionID != "" {
		if c, err := h.DB.GetGitConnection(ctx, connectionID); err == nil {
			return toGitConnection(c)
		}
		// A connection that was deleted out from under an app falls through to the
		// host lookup rather than failing: the app still names a real repository.
		log.Printf("[git] app references unknown connection %s; falling back to host lookup", connectionID)
	}

	c, err := h.DB.FindGitConnectionForRepo(ctx, ref.Provider, ref.Host, ref.Owner())
	if err != nil {
		// No row yet -- an install that has not adopted, or a provider with no connection.
		// The synthesised value carries enough for a provider that reads only the host.
		return git.Connection{Provider: ref.Provider, Host: ref.Host}
	}
	return toGitConnection(c)
}

func toGitConnection(c db.GitConnection) git.Connection {
	return git.Connection{
		ID:          c.ID,
		Provider:    c.Provider,
		DisplayName: c.DisplayName,
		BaseURL:     c.BaseURL,
		Host:        c.Host,
		Account:     c.Account,
		SecretName:  c.SecretName,
		ExternalID:  c.ExternalID,
		Metadata:    c.Metadata,
	}
}

// ListGitConnections returns every configured connection.
func (h *Handler) ListGitConnections(c *gin.Context) {
	conns, err := h.DB.ListGitConnections(c.Request.Context(), c.Query("provider"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	type item struct {
		db.GitConnection
		InstallURL  string `json:"installUrl,omitempty"`
		WebhookPath string `json:"webhookPath"`
	}

	out := make([]item, 0, len(conns))
	for _, conn := range conns {
		it := item{
			GitConnection: conn,
			WebhookPath:   "/api/v1/webhooks/" + conn.Provider + "/" + conn.ID,
		}
		if p, ok := h.GitProviders.Get(conn.Provider); ok {
			it.InstallURL = p.InstallURL(toGitConnection(conn))
		}
		out = append(out, it)
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: out, Total: len(out)})
}

// DeleteGitConnection removes a connection.
//
// The credential Secret is deleted with it, but only when nothing else points at it -- the
// adopted GitHub App shares its Secret with the settings page's own view of it.
func (h *Handler) DeleteGitConnection(c *gin.Context) {
	id := c.Param("connectionId")

	conn, err := h.DB.GetGitConnection(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "connection not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if err := h.DB.DeleteGitConnection(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if n, err := h.DB.CountGitConnectionsBySecret(c.Request.Context(), conn.SecretName); err == nil && n == 0 {
		if err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
			Delete(c.Request.Context(), conn.SecretName, metav1.DeleteOptions{}); err != nil {
			log.Printf("[git] connection %s removed but its secret %s was not: %v", id, conn.SecretName, err)
		}
	}

	// The adopted connection and the legacy GitHub App service are two views of one Secret,
	// so dropping the row has to unconfigure the service as well or it keeps serving from
	// credentials the user just removed.
	if h.GitHubApp != nil {
		h.GitHubApp.ForgetIdentity(conn.SecretName)
		if conn.IsLegacy && conn.Provider == git.ProviderGitHub {
			h.GitHubApp.Unconfigure()
		}
	}

	h.auditLog(c, "delete_git_connection", "git_connection", id, conn.DisplayName, "", "",
		map[string]interface{}{"provider": conn.Provider, "host": conn.Host})

	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

// resolveWebhookConnection works out which connection a delivery belongs to.
//
// An explicit id in the URL wins. Without one this is a hook created before connections
// existed, which can only be the adopted row; if nothing has been adopted, the install has
// no GitHub App configured and there is nothing to verify against.
func (h *Handler) resolveWebhookConnection(ctx context.Context, provider, connectionID string) (git.Connection, error) {
	if connectionID != "" {
		c, err := h.DB.GetGitConnection(ctx, connectionID)
		if err != nil {
			return git.Connection{}, fmt.Errorf("unknown connection")
		}
		if c.Provider != provider {
			// The URL says one provider and the row another, so one of them is wrong and
			// guessing which would mean verifying with the wrong scheme.
			return git.Connection{}, fmt.Errorf("connection is not a %s connection", provider)
		}
		return toGitConnection(c), nil
	}

	c, err := h.DB.GetLegacyGitConnection(ctx, provider)
	if err != nil {
		// Nothing adopted. Fall back to a bare connection so an install with no
		// connections at all behaves as it did -- webhookSecretFor then finds no secret,
		// and the unsigned-delivery rules take it from there.
		return git.Connection{Provider: provider, Host: git.DefaultHost(provider)}, nil
	}
	return toGitConnection(c), nil
}

// webhookSecretFor reads the shared secret for a connection out of its Kubernetes Secret.
//
// The legacy connection's secret is held in memory by the GitHub App service, so it is read
// from there; everything else is read from the Secret the connection names.
func (h *Handler) webhookSecretFor(ctx context.Context, conn git.Connection) string {
	if conn.SecretName == "" {
		if conn.Provider == git.ProviderGitHub && h.GitHubApp != nil && h.GitHubApp.IsConfigured() {
			return h.GitHubApp.WebhookSecret()
		}
		return ""
	}

	// The service already caches credentials per Secret, so go through it rather than
	// reading the Secret a second time here.
	if h.GitHubApp != nil && conn.Provider == git.ProviderGitHub {
		return h.GitHubApp.WebhookSecretVia(ctx, conn.SecretName)
	}

	secret, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
		Get(ctx, conn.SecretName, metav1.GetOptions{})
	if err != nil {
		log.Printf("[git] connection %s names an unreadable secret %s: %v", conn.ID, conn.SecretName, err)
		return ""
	}
	return string(secret.Data["webhook-secret"])
}

// CreateGitConnection adds a token-based connection.
//
// GitLab and Bitbucket have no equivalent of GitHub's App-manifest handshake: access follows
// a token's scope, so connecting is just storing a token and saying where it points. GitHub
// keeps its own wizard, because an App is created rather than pasted.
func (h *Handler) CreateGitConnection(c *gin.Context) {
	var req struct {
		Provider      string `json:"provider" binding:"required"`
		DisplayName   string `json:"displayName"`
		BaseURL       string `json:"baseUrl"`
		Account       string `json:"account"`
		Token         string `json:"token" binding:"required"`
		WebhookSecret string `json:"webhookSecret"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// GitHub is excluded on purpose: creating an App is a handshake with GitHub, and a
	// pasted token would produce a connection that cannot mint installation tokens.
	if req.Provider != git.ProviderGitLab && req.Provider != git.ProviderBitbucket {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code: 400, Message: "only gitlab and bitbucket connections are created this way; use the GitHub App flow for github"})
		return
	}
	if _, ok := h.GitProviders.Get(req.Provider); !ok {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "unsupported provider"})
		return
	}

	host, baseURL, err := connectionEndpoint(req.Provider, req.BaseURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	connectionID := uuid.NewString()
	secretName := "vesta-git-" + connectionID

	// The token goes into a Secret, never into the row. A connection can then be listed,
	// logged and returned to the UI without a credential travelling with it -- the same
	// rule the middleware and log-drain features already follow.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: vestaSystemNS,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vesta",
				"app.kubernetes.io/component":  "git-connection",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"token":          req.Token,
			"webhook-secret": req.WebhookSecret,
		},
	}
	if _, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
		Create(c.Request.Context(), secret, metav1.CreateOptions{}); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Code: 500, Message: "could not store the token: " + err.Error()})
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = host
	}

	conn, err := h.DB.CreateGitConnection(c.Request.Context(), db.GitConnection{
		ID:          connectionID,
		Provider:    req.Provider,
		DisplayName: displayName,
		BaseURL:     baseURL,
		Host:        host,
		Account:     strings.TrimSpace(req.Account),
		SecretName:  secretName,
	})
	if err != nil {
		// Do not leave a token behind for a connection that does not exist.
		_ = h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
			Delete(c.Request.Context(), secretName, metav1.DeleteOptions{})

		if errors.Is(err, db.ErrDuplicate) {
			c.JSON(http.StatusConflict, models.ErrorResponse{
				Code: 409, Message: "a connection to that host and account already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "create_git_connection", "git_connection", conn.ID, conn.DisplayName, "", "",
		map[string]interface{}{"provider": conn.Provider, "host": conn.Host})

	c.JSON(http.StatusCreated, gin.H{
		"id":          conn.ID,
		"provider":    conn.Provider,
		"displayName": conn.DisplayName,
		"host":        conn.Host,
		// The URL this connection's webhooks must POST to. Shown once here and again in the
		// connection list, because a hook pointed at the wrong one silently never fires.
		"webhookPath": "/api/v1/webhooks/" + conn.Provider + "/" + conn.ID,
	})
}

// connectionEndpoint works out the host and API base for a connection.
//
// An empty base URL means the provider's SaaS. Anything else is self-managed, and the host
// is derived from it rather than asked for separately -- two fields that must agree are two
// fields that can disagree.
func connectionEndpoint(provider, baseURL string) (host, normalized string, err error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return git.DefaultHost(provider), "", nil
	}

	// The scheme is added before any trimming. Trimming first turns "https://" into
	// "https:/", which no longer looks like it has a scheme, so it would be prefixed again
	// into "https://https:/" and parse to a host of "https:".
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Host == "" {
		return "", "", fmt.Errorf("%q is not a usable URL", baseURL)
	}

	u.Path = strings.TrimSuffix(u.Path, "/")
	return strings.ToLower(u.Host), strings.TrimSuffix(u.String(), "/"), nil
}
