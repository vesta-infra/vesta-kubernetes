package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/handlers"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/middleware"
	"kubernetes.getvesta.sh/api/internal/rbac"
	"kubernetes.getvesta.sh/api/internal/services"
)

// configureTrustedProxies restricts which hops gin will believe X-Forwarded-For from.
// gin trusts every proxy by default, which makes c.ClientIP() - and so every audit log
// entry and any IP-keyed throttle - attacker-controlled. VESTA_TRUSTED_PROXIES takes a
// comma-separated list of CIDRs or IPs (typically the ingress controller's range).
// Unset means trust nothing, so c.ClientIP() reports the direct peer: less useful behind
// an ingress, but never forgeable.
func configureTrustedProxies(r *gin.Engine) error {
	raw := strings.TrimSpace(os.Getenv("VESTA_TRUSTED_PROXIES"))
	if raw == "" {
		log.Println("VESTA_TRUSTED_PROXIES is unset: trusting no proxies, client IPs will be the direct peer address")
		return r.SetTrustedProxies(nil)
	}

	var proxies []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			proxies = append(proxies, p)
		}
	}
	return r.SetTrustedProxies(proxies)
}

// registerMFARoutes wires the two-factor endpoints.
//
// Extracted from main so a test can register exactly what production does and check it
// against the allowlist, rather than a second copy of the same paths that could agree
// with the test while disagreeing with the server.
//
// The paths must match middleware.challengeRoutes and enrollRoutes exactly: those
// allowlists are what a partially-authenticated token is measured against, and a path
// missing from them is unreachable during the login it exists to complete.
func registerMFARoutes(auth *gin.RouterGroup, h *handlers.Handler) {
	auth.GET("/auth/mfa/status", h.GetMFAStatus)
	auth.POST("/auth/mfa/verify", h.VerifyMFA)
	auth.POST("/auth/mfa/totp/enroll", h.EnrollTOTP)
	auth.POST("/auth/mfa/totp/confirm", h.ConfirmTOTP)
	auth.DELETE("/auth/mfa/totp", h.DisableTOTP)
	auth.GET("/auth/mfa/webauthn/credentials", h.ListWebAuthnCredentials)
	auth.POST("/auth/mfa/webauthn/register/begin", h.BeginWebAuthnRegistration)
	auth.POST("/auth/mfa/webauthn/register/finish", h.FinishWebAuthnRegistration)
	auth.POST("/auth/mfa/webauthn/authenticate/begin", h.BeginWebAuthnAuthentication)
	auth.POST("/auth/mfa/webauthn/authenticate/finish", h.FinishWebAuthnAuthentication)
	auth.PUT("/auth/mfa/webauthn/credentials/:id", h.RenameWebAuthnCredential)
	auth.DELETE("/auth/mfa/webauthn/credentials/:id", h.DeleteWebAuthnCredential)
	auth.POST("/auth/mfa/backup-codes", h.RegenerateBackupCodes)

	// Proving it is still you, before a change that could remove your own protection.
	// Not on the partial-token allowlist: these are only reachable with a real session.
	auth.POST("/auth/reauth/password", h.ReauthWithPassword)
	auth.POST("/auth/reauth/webauthn/begin", h.BeginReauthWebAuthn)
	auth.POST("/auth/reauth/webauthn/finish", h.FinishReauthWebAuthn)

	// Clearing a user's factors when they have lost every way of producing one.
	auth.DELETE("/users/:userId/mfa", middleware.RequireRole("admin"), h.ResetUserMFA)

	// Who must carry a second factor. Admin-configurable so a policy change needs no
	// redeploy; readable by anyone, because the enrollment screen has to explain why it
	// is being shown.
	auth.GET("/settings/mfa-policy", h.GetMFAPolicy)
	auth.PUT("/settings/mfa-policy", middleware.RequireRole("admin"), h.UpdateMFAPolicy)
}

// registerSystemRoutes wires version reporting and self-update.
//
// Reading the version is open to any authenticated user so the UI can display it.
// Changing it is admin-only and additionally costs a re-authentication grant inside the
// handler, because pointing Vesta's own deployments at another version is close to
// handing over cluster-admin.
func registerSystemRoutes(auth *gin.RouterGroup, h *handlers.Handler) {
	auth.GET("/system/version", h.GetSystemVersion)
	auth.GET("/system/update", h.GetUpdateStatus)
	auth.GET("/system/update/status", h.GetUpdateProgress)
	auth.POST("/system/update/check", middleware.RequireRole("admin"), h.CheckForUpdates)
	auth.PUT("/system/update/settings", middleware.RequireRole("admin"), h.UpdateSettings)
	auth.POST("/system/update", middleware.RequireRole("admin"), h.TriggerUpdate)
}

// verifyPartialAuthRoutes asserts that every route a half-authenticated token is allowed
// to reach actually exists on the router.
//
// The allowlist in middleware and the route registrations here live in different files
// and are edited independently. A typo in either produces no error at all -- it produces
// a login that gets as far as "enter your code" and then 404s -- so the two are compared
// once at startup, where the failure is loud and immediate.
func verifyPartialAuthRoutes(r *gin.Engine) error {
	registered := make(map[string]bool)
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	var missing []string
	for _, spec := range middleware.PartialAuthRoutes() {
		if !registered[spec.Method+" "+spec.Pattern] {
			missing = append(missing, spec.Method+" "+spec.Pattern)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("two-factor allowlist names routes that are not registered: %s", strings.Join(missing, ", "))
	}
	return nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	if err := middleware.InitJWTSecret(); err != nil {
		log.Fatalf("Failed to load JWT signing key: %v", err)
	}

	database, err := db.New(databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	kc, err := k8s.NewClient()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	notifier := services.NewNotifier(database)

	h := handlers.New(kc, database, notifier)

	// Record any pre-existing GitHub App as a connection, so one install's worth of
	// history keeps working: webhooks registered before this release carry no connection
	// in their URL and resolve to this row.
	handlers.AdoptLegacyGitHubApp(context.Background(), database, h.GitHubApp)

	r := gin.Default()

	if err := configureTrustedProxies(r); err != nil {
		log.Fatalf("Failed to configure trusted proxies: %v", err)
	}

	// Liveness: is the process up. Deliberately trivial -- a liveness probe that checks
	// dependencies restarts the pod when the database hiccups, which helps nobody.
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Readiness: can this process actually serve. This one has to check dependencies,
	// because it is what makes a bad upgrade fail safely. With a probe that always says
	// yes, a new image that cannot reach Postgres still goes Ready, the rollout
	// completes, the old ReplicaSet scales to zero, and the component that would perform
	// the rollback is the broken one -- an update triggered from the UI could brick the
	// UI it was triggered from.
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		if err := database.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "reason": "database unreachable"})
			return
		}
		if _, err := kc.Clientset.Discovery().ServerVersion(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready", "reason": "kubernetes API unreachable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	v1 := r.Group("/api/v1")

	// Setup (unauthenticated)
	v1.GET("/setup/status", h.SetupStatus)
	v1.POST("/setup", h.Setup)

	// Auth (unauthenticated)
	v1.POST("/auth/login", h.Login)
	v1.GET("/auth/oauth/:provider", h.OAuthRedirect)
	v1.GET("/auth/forgot-password/status", h.ForgotPasswordStatus)
	v1.POST("/auth/forgot-password", h.ForgotPassword)
	v1.POST("/auth/reset-password", h.ResetPassword)
	v1.POST("/auth/accept-invite", h.AcceptInvite)

	// Webhooks (unauthenticated, verified by signature)
	v1.POST("/webhooks/:provider", h.ReceiveWebhook)
	// Per-connection webhook URL. The path above stays for hooks created before
	// connections existed; this one is what new connections register.
	v1.POST("/webhooks/:provider/:connectionId", h.ReceiveWebhook)

	// GitHub App manifest flow (callback is unauthenticated, state-verified)
	v1.GET("/github/callback", h.GitHubAppCallback)

	// Authenticated routes.
	//
	// RequireFullSession must be registered here, before any route is added to the group:
	// gin only propagates Use() to routes registered afterwards. Applied once it covers
	// every endpoint below, including the WebSocket routes that take their token from a
	// query parameter, and any route added later is protected without anyone having to
	// remember. It rejects tokens issued mid-authentication - after a password but before
	// a second factor - which would otherwise be honoured as full sessions.
	auth := v1.Group("")
	auth.Use(middleware.AuthRequired(database), middleware.RequireFullSession())
	dv := middleware.DenyRole("viewer") // deny viewer access to write endpoints

	// Project and environment scoped access. These sit alongside dv rather than replacing
	// it: both must pass, and while rbac.enforcement is off they resolve to exactly the
	// behaviour dv already gave, so adding them changes nothing until an admin turns
	// enforcement on.
	canRead := middleware.RequireAccess(database, h.Scope, rbac.ActionRead)
	canDeploy := middleware.RequireAccess(database, h.Scope, rbac.ActionDeploy)
	canWrite := middleware.RequireAccess(database, h.Scope, rbac.ActionWrite)
	// Secrets, exec and pod files share a level: reading a secret, running a shell in the
	// pod and reading a file off its filesystem are one capability by three routes.
	canSecrets := middleware.RequireAccess(database, h.Scope, rbac.ActionSecrets)
	canAdmin := middleware.RequireAccess(database, h.Scope, rbac.ActionAdmin)
	{
		// User profile
		auth.GET("/users/me", h.GetCurrentUser)
		auth.GET("/users/me/permissions", h.GetMyPermissions)
		auth.PUT("/users/me", h.UpdateProfile)
		auth.PUT("/users/me/password", h.ChangePassword)

		// User management (admin only)
		auth.GET("/users", middleware.RequireRole("admin"), h.ListUsers)
		auth.POST("/auth/register", middleware.RequireRole("admin"), h.Register)

		// Teams
		auth.GET("/teams", h.ListTeams)
		auth.POST("/teams", middleware.RequireRole("admin"), h.CreateTeam)
		auth.GET("/teams/:teamId", h.GetTeam)
		auth.PUT("/teams/:teamId", middleware.RequireTeamRole(database, "owner", "admin"), h.UpdateTeam)
		auth.DELETE("/teams/:teamId", middleware.RequireRole("admin"), h.DeleteTeam)
		auth.POST("/teams/:teamId/members", middleware.RequireTeamRole(database, "owner", "admin"), h.AddTeamMember)
		auth.DELETE("/teams/:teamId/members/:userId", middleware.RequireTeamRole(database, "owner", "admin"), h.RemoveTeamMember)

		// Projects
		auth.POST("/projects", dv, h.CreateProject)
		auth.GET("/projects", h.ListProjects)
		auth.GET("/projects/:projectId", canRead, h.GetProject)
		auth.PUT("/projects/:projectId", canWrite, dv, h.UpdateProject)
		auth.DELETE("/projects/:projectId", canWrite, dv, h.DeleteProject)

		// Environments
		auth.POST("/projects/:projectId/environments", canWrite, dv, h.CreateEnvironment)
		auth.GET("/projects/:projectId/environments", canRead, h.ListEnvironments)
		auth.PUT("/projects/:projectId/environments/:env", canWrite, dv, h.UpdateEnvironment)
		auth.DELETE("/projects/:projectId/environments/:env", canWrite, dv, h.DeleteEnvironment)
		auth.POST("/projects/:projectId/environments/:env/clone", canWrite, dv, h.CloneEnvironment)

		// Apps
		auth.GET("/pod-sizes", h.ListPodSizes)
		auth.POST("/projects/:projectId/apps", canWrite, dv, middleware.RequireScope("write"), h.CreateApp)
		auth.GET("/projects/:projectId/apps", canRead, middleware.RequireScope("read"), h.ListProjectApps)
		auth.GET("/apps", middleware.RequireScope("read"), h.ListApps)
		auth.GET("/apps/:appId", canRead, middleware.RequireScope("read"), h.GetApp)
		auth.PUT("/apps/:appId", canWrite, dv, middleware.RequireScope("write"), h.UpdateApp)
		auth.DELETE("/apps/:appId", canWrite, dv, middleware.RequireScope("write"), h.DeleteApp)
		auth.POST("/apps/:appId/clone", canWrite, dv, middleware.RequireScope("write"), h.CloneApp)

		// Deploy
		auth.POST("/apps/:appId/deploy", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.DeployApp)
		auth.POST("/apps/:appId/rollback", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.RollbackApp)
		auth.GET("/apps/:appId/deployments", canDeploy, middleware.RequireScope("read"), h.ListDeployments)
		auth.POST("/apps/:appId/restart", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.RestartApp)
		auth.POST("/apps/:appId/scale", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.ScaleApp)
		auth.POST("/apps/:appId/sleep", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.SleepApp)
		auth.POST("/apps/:appId/wake", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.WakeApp)
		auth.POST("/apps/:appId/stop", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.StopApp)
		auth.POST("/apps/:appId/start", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.StartApp)

		// Cronjob management
		auth.POST("/apps/:appId/cronjobs/:name/trigger", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.TriggerCronJob)
		auth.GET("/apps/:appId/cronjobs/status", canRead, middleware.RequireScope("read"), h.GetCronJobStatuses)

		// Pod file browser (requires developer+ role — exec into pods can expose secrets)
		auth.GET("/apps/:appId/files", canSecrets, dv, middleware.RequireScope("read"), h.ListPodFiles)
		auth.GET("/apps/:appId/files/read", canSecrets, dv, middleware.RequireScope("read"), h.ReadPodFile)
		auth.POST("/apps/:appId/files/write", canSecrets, dv, middleware.RequireScope("write"), h.WritePodFile)

		// Rate limiting
		auth.GET("/apps/:appId/rate-limits", canRead, middleware.RequireScope("read"), h.GetRateLimits)
		auth.PUT("/apps/:appId/rate-limits", canWrite, dv, middleware.RequireScope("write"), h.UpdateRateLimits)

		// Middlewares are defined once in vesta-system and attached to an app's
		// environments in order. Deleting one still attached is refused by the handler,
		// since Traefik drops a whole router whose middleware is missing.
		auth.GET("/middlewares", middleware.RequireScope("read"), h.ListMiddlewares)
		auth.GET("/middlewares/:name", middleware.RequireScope("read"), h.GetMiddleware)
		auth.POST("/middlewares", dv, middleware.RequireScope("write"), h.CreateMiddleware)
		auth.PUT("/middlewares/:name", dv, middleware.RequireScope("write"), h.UpdateMiddleware)
		auth.DELETE("/middlewares/:name", dv, middleware.RequireScope("write"), h.DeleteMiddleware)
		// Log drains ship app logs to external destinations. Scope decides which apps ship
		// where, so there is no per-app attach endpoint -- an app-scoped drain is the
		// attachment.
		auth.GET("/log-drains", middleware.RequireScope("read"), h.ListLogDrains)
		auth.GET("/log-drains/:name", middleware.RequireScope("read"), h.GetLogDrain)
		auth.POST("/log-drains", dv, middleware.RequireScope("write"), h.CreateLogDrain)
		auth.PUT("/log-drains/:name", dv, middleware.RequireScope("write"), h.UpdateLogDrain)
		auth.DELETE("/log-drains/:name", dv, middleware.RequireScope("write"), h.DeleteLogDrain)

		auth.GET("/apps/:appId/middlewares", canRead, middleware.RequireScope("read"), h.GetAppMiddlewares)
		auth.PUT("/apps/:appId/middlewares", canWrite, dv, middleware.RequireScope("write"), h.UpdateAppMiddlewares)

		// Builds
		auth.POST("/apps/:appId/builds", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.TriggerBuild)
		auth.GET("/apps/:appId/builds", canRead, middleware.RequireScope("read"), h.ListBuilds)
		auth.GET("/apps/:appId/builds/:buildId", canRead, middleware.RequireScope("read"), h.GetBuild)
		auth.GET("/apps/:appId/builds/:buildId/logs", canRead, middleware.RequireScope("read"), h.GetBuildLogs)
		auth.POST("/apps/:appId/builds/:buildId/cancel", canDeploy, dv, middleware.RequireScope("deploy", "write"), h.CancelBuild)

		// Environment Variables (per app per environment) -- non-secret config
		auth.POST("/apps/:appId/envs/:env/envvars", canWrite, dv, h.CreateAppEnvVars)
		auth.GET("/apps/:appId/envs/:env/envvars", canWrite, dv, h.ListAppEnvVars)
		auth.DELETE("/apps/:appId/envs/:env/envvars/:key", canWrite, dv, h.DeleteAppEnvVarKey)

		// Secrets (per app per environment) -- viewers have no access
		auth.POST("/apps/:appId/envs/:env/secrets", canSecrets, dv, h.CreateAppEnvSecret)
		auth.GET("/apps/:appId/envs/:env/secrets", canSecrets, dv, h.ListAppEnvSecrets)
		auth.DELETE("/apps/:appId/envs/:env/secrets/:key", canSecrets, dv, h.DeleteAppEnvSecretKey)
		auth.GET("/apps/:appId/envs/:env/secrets/reveal", canSecrets, dv, h.RevealAppEnvSecretValues)
		auth.GET("/secrets", dv, h.ListSecrets)
		auth.GET("/secrets/:secretId/reveal", dv, h.RevealSecretValues)
		auth.PUT("/secrets/:secretId", dv, h.UpdateSecret)
		auth.DELETE("/secrets/:secretId", dv, h.DeleteSecret)
		auth.POST("/secrets/registry", dv, h.CreateRegistrySecret)
		auth.GET("/secrets/registry", dv, h.ListRegistrySecrets)
		auth.DELETE("/secrets/registry/:name", dv, h.DeleteRegistrySecret)
		// Browsing a registry through a stored credential, so images and tags can be
		// picked rather than typed.
		auth.GET("/secrets/registry/:name/repositories", dv, h.ListRegistryRepositories)
		auth.GET("/secrets/registry/:name/tags", dv, h.ListRegistryTags)
		auth.POST("/secrets/registry/:name/test", dv, h.TestRegistryCredential)

		// Shared Secrets (project-scoped, opt-in per app)
		auth.POST("/projects/:projectId/shared-secrets", canSecrets, dv, h.CreateSharedSecret)
		auth.GET("/projects/:projectId/shared-secrets", canSecrets, h.ListSharedSecrets)
		auth.PUT("/projects/:projectId/shared-secrets/:name", canSecrets, dv, h.UpdateSharedSecret)
		auth.GET("/projects/:projectId/shared-secrets/:name/reveal", canSecrets, middleware.RequireProjectRole(database, "owner"), h.RevealSharedSecret)
		auth.DELETE("/projects/:projectId/shared-secrets/:name", canSecrets, dv, h.DeleteSharedSecret)
		auth.POST("/apps/:appId/shared-secrets", canSecrets, dv, h.BindSharedSecret)
		auth.GET("/apps/:appId/shared-secrets", canSecrets, h.ListAppSharedSecrets)
		auth.DELETE("/apps/:appId/shared-secrets/:name", canSecrets, dv, h.UnbindSharedSecret)

		registerMFARoutes(auth, h)
		registerSystemRoutes(auth, h)

		// Project transfer between Vesta instances. Export is gated like a secret
		// reveal because the bundle contains every secret in the project; import is
		// admin-only because it creates instance-level registry credentials.
		auth.GET("/instance/identity", h.GetInstanceIdentity)
		auth.POST("/projects/import", middleware.RequireRole("admin"), h.ImportProject)
		auth.POST("/projects/:projectId/export", canSecrets, dv, h.ExportProject)

		// Project Members (owner management)
		auth.GET("/projects/:projectId/members", canRead, h.ListProjectMembers)
		auth.POST("/projects/:projectId/members", canAdmin, h.AddProjectMember)
		auth.DELETE("/projects/:projectId/members/:userId", canAdmin, h.RemoveProjectMember)

		// Environment-scoped roles. A row here overrides the project role in both
		// directions, which is what makes "maintainer on the project, viewer on
		// production" expressible.
		auth.GET("/projects/:projectId/env-members", canRead, h.ListProjectEnvMembers)
		auth.PUT("/projects/:projectId/environments/:env/members/:userId", canAdmin, h.SetProjectEnvRole)
		auth.DELETE("/projects/:projectId/environments/:env/members/:userId", canAdmin, h.RemoveProjectEnvRole)

		// Logs and monitoring
		auth.GET("/apps/:appId/diagnostics", canRead, middleware.RequireScope("read"), h.GetAppDiagnostics)
		auth.GET("/apps/:appId/logs", canRead, h.StreamLogs)
		auth.GET("/apps/:appId/logs/ws", canRead, h.StreamLogsWS)
		auth.GET("/apps/:appId/exec", canSecrets, dv, h.ExecWS)
		auth.GET("/apps/:appId/metrics", canRead, h.GetMetrics)
		auth.GET("/apps/:appId/metrics/prometheus", canRead, h.GetPrometheusMetrics)
		auth.GET("/metrics/prometheus/status", h.GetPrometheusStatus)

		// Templates
		auth.GET("/templates", h.ListTemplates)
		auth.POST("/templates/:id/deploy", dv, h.DeployTemplate)

		// Health Dashboard
		auth.GET("/health/dashboard", h.GetHealthDashboard)

		// Notifications -- viewers can see channels and history but not manage
		auth.POST("/projects/:projectId/notifications", canWrite, dv, h.CreateNotificationChannel)
		auth.GET("/projects/:projectId/notifications", canRead, h.ListNotificationChannels)
		auth.PUT("/projects/:projectId/notifications/:channelId", canWrite, dv, h.UpdateNotificationChannel)
		auth.DELETE("/projects/:projectId/notifications/:channelId", canWrite, dv, h.DeleteNotificationChannel)
		auth.POST("/projects/:projectId/notifications/:channelId/test", canWrite, dv, h.TestNotificationChannel)
		auth.GET("/projects/:projectId/notifications/history", canRead, h.ListNotificationHistory)

		// Alert rules
		auth.POST("/projects/:projectId/alerts", canWrite, dv, h.CreateAlertRule)
		auth.GET("/projects/:projectId/alerts", canRead, h.ListAlertRules)
		auth.PUT("/projects/:projectId/alerts/:ruleId", canWrite, dv, h.UpdateAlertRule)
		auth.DELETE("/projects/:projectId/alerts/:ruleId", canWrite, dv, h.DeleteAlertRule)

		// Dependencies
		auth.GET("/projects/:projectId/dependencies", canRead, h.GetAppDependencies)

		// Managed add-ons. Project-scoped, which is also what keeps them inside the
		// route-coverage guard's reach.
		// Readable by any authenticated user, writable only by an admin.
		//
		// The asymmetry is deliberate. Under the restricted profile an app runs as a
		// non-root user with a read-only filesystem, and an image not built for that
		// crashes on deploy -- so a developer who cannot see the profile has no way to
		// explain their own app's failure. Withholding it buys nothing against a caller
		// who is already authenticated and can read the app's pods.
		auth.GET("/settings/security", h.GetSecurityPosture)
		auth.PUT("/settings/security", middleware.RequireRole("admin"), h.SetSecurityPosture)

		auth.GET("/projects/:projectId/environments/:env/quota", canRead, h.GetEnvironmentQuota)
		auth.PUT("/projects/:projectId/environments/:env/quota", canAdmin, h.SetEnvironmentQuota)

		auth.GET("/projects/:projectId/costs", canRead, h.GetProjectCosts)
		auth.GET("/apps/:appId/costs", canRead, h.GetAppCosts)

		auth.GET("/projects/:projectId/addons", canRead, h.ListAddons)
		auth.POST("/projects/:projectId/addons", canWrite, h.CreateAddon)
		auth.DELETE("/projects/:projectId/addons/:name", canWrite, h.DeleteAddon)
		// Revealing connection details hands over a password, so it is gated like a secret.
		auth.GET("/projects/:projectId/addons/:name/credentials", canSecrets, h.RevealAddonCredentials)

		auth.POST("/apps/:appId/addons", canWrite, h.BindAddon)
		auth.DELETE("/apps/:appId/addons/:name", canWrite, h.UnbindAddon)

		// Scheduled deployments
		auth.POST("/projects/:projectId/scheduled-deployments", canDeploy, dv, h.CreateScheduledDeployment)
		auth.GET("/projects/:projectId/scheduled-deployments", canRead, h.ListScheduledDeployments)
		auth.DELETE("/projects/:projectId/scheduled-deployments/:deploymentId", canDeploy, dv, h.CancelScheduledDeployment)

		// API tokens
		auth.GET("/auth/tokens", h.ListAPITokens)
		auth.POST("/auth/tokens", h.CreateAPIToken)
		auth.DELETE("/auth/tokens/:id", h.RevokeAPIToken)

		// Audit log (admin only - it spans every project and records auth events).
		// /activity stays open to all roles but scopes its results per caller.
		auth.GET("/settings/rbac", middleware.RequireRole("admin"), h.GetRBACSettings)
		auth.PUT("/settings/rbac", middleware.RequireRole("admin"), h.UpdateRBACSettings)

		auth.GET("/audit-logs", middleware.RequireRole("admin"), h.ListAuditLogs)
		auth.GET("/activity", h.GetActivityFeed)

		// Webhook delivery log (admin only)
		auth.GET("/webhook-deliveries", middleware.RequireRole("admin"), h.ListWebhookDeliveries)

		// SSL certificate providers (admin only). These create and manage cert-manager
		// ClusterIssuers, so they are gated on the global admin role rather than a scope.
		// The dashboard's own hostname and certificate. Admin only: a wrong hostname
		// makes the UI unreachable except by port-forward.
		auth.GET("/settings/ui-domain", middleware.RequireRole("admin"), h.GetUIDomain)
		auth.PUT("/settings/ui-domain", middleware.RequireRole("admin"), h.UpdateUIDomain)

		auth.GET("/settings/ssl-providers", middleware.RequireRole("admin"), h.ListSSLProviders)
		auth.POST("/settings/ssl-providers", middleware.RequireRole("admin"), h.CreateSSLProvider)
		auth.PUT("/settings/ssl-providers/default", middleware.RequireRole("admin"), h.SetDefaultSSLProvider)
		auth.GET("/settings/ssl-providers/status", middleware.RequireRole("admin"), h.GetCertManagerStatus)
		auth.PUT("/settings/ssl-providers/:name", middleware.RequireRole("admin"), h.UpdateSSLProvider)
		auth.DELETE("/settings/ssl-providers/:name", middleware.RequireRole("admin"), h.DeleteSSLProvider)

		// GitHub App settings (admin only)
		auth.POST("/github/manifest", middleware.RequireRole("admin"), h.GetGitHubAppManifest)
		auth.GET("/settings/github-app", middleware.RequireRole("admin"), h.GetGitHubAppStatus)
		auth.GET("/settings/github-app/installations", middleware.RequireRole("admin"), h.ListGitHubAppInstallations)
		auth.DELETE("/settings/github-app", middleware.RequireRole("admin"), h.DeleteGitHubApp)

		// Git connections. Several may exist at once, across providers.
		auth.GET("/settings/git-connections", middleware.RequireRole("admin"), h.ListGitConnections)
		auth.POST("/settings/git-connections", middleware.RequireRole("admin"), h.CreateGitConnection)
		auth.DELETE("/settings/git-connections/:connectionId", middleware.RequireRole("admin"), h.DeleteGitConnection)

		// Git helpers
		auth.GET("/git/branches", dv, h.ListRepoBranches)
		auth.GET("/git/repos", dv, h.ListAccessibleRepos)
	}

	if err := verifyPartialAuthRoutes(r); err != nil {
		log.Fatal(err)
	}

	log.Printf("Vesta API server starting on :%s", port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start scheduled deployment worker
	scheduler := &services.ScheduledDeploymentWorker{DB: database, K8s: kc}
	go scheduler.Start(ctx)

	// Keeps the newest published release in the settings table so the UI can compare it
	// against what is running without reaching the network on every page load.
	go (&services.UpdateChecker{DB: database}).Start(ctx)

	// Scales idle apps to zero. Here rather than in the operator because the Prometheus
	// client and its query already live here -- and because the failure asymmetry is right
	// this way round: if the API is down, nothing sleeps, and it can never prevent a wake.
	sweeper := services.NewSleepSweeper(database, kc)
	h.Sleep = sweeper
	go sweeper.Start(ctx)

	// Records what workloads reserve, so cost can be reported without a billing API. Works
	// with no Prometheus: a Deployment's replica count and its containers' requests answer
	// "what is this reserving" completely, which is the whole basis of the figure.
	go services.NewCostSampler(database, kc).Start(ctx)

	// An upgrade replaces this pod, so the process that started one is rarely the process
	// that sees it finish. Close out whatever the previous process left behind, or a
	// record stuck at "running" blocks every future upgrade.
	h.Updater.ReconcileOnStartup(context.Background(), handlers.ReleaseNamespace())

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	// Drain in-flight requests rather than cutting them. This matters most during an
	// upgrade, which SIGTERMs this pod while someone is very likely watching the upgrade
	// status endpoint.
	log.Println("shutting down, draining in-flight requests...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
