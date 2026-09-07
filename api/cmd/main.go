package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/handlers"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/middleware"
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

	r := gin.Default()

	if err := configureTrustedProxies(r); err != nil {
		log.Fatalf("Failed to configure trusted proxies: %v", err)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
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
	{
		// User profile
		auth.GET("/users/me", h.GetCurrentUser)
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
		auth.GET("/projects/:projectId", h.GetProject)
		auth.PUT("/projects/:projectId", dv, h.UpdateProject)
		auth.DELETE("/projects/:projectId", dv, h.DeleteProject)

		// Environments
		auth.POST("/projects/:projectId/environments", dv, h.CreateEnvironment)
		auth.GET("/projects/:projectId/environments", h.ListEnvironments)
		auth.PUT("/projects/:projectId/environments/:env", dv, h.UpdateEnvironment)
		auth.DELETE("/projects/:projectId/environments/:env", dv, h.DeleteEnvironment)
		auth.POST("/projects/:projectId/environments/:env/clone", dv, h.CloneEnvironment)

		// Apps
		auth.GET("/pod-sizes", h.ListPodSizes)
		auth.POST("/projects/:projectId/apps", dv, middleware.RequireScope("write"), h.CreateApp)
		auth.GET("/projects/:projectId/apps", middleware.RequireScope("read"), h.ListProjectApps)
		auth.GET("/apps", middleware.RequireScope("read"), h.ListApps)
		auth.GET("/apps/:appId", middleware.RequireScope("read"), h.GetApp)
		auth.PUT("/apps/:appId", dv, middleware.RequireScope("write"), h.UpdateApp)
		auth.DELETE("/apps/:appId", dv, middleware.RequireScope("write"), h.DeleteApp)
		auth.POST("/apps/:appId/clone", dv, middleware.RequireScope("write"), h.CloneApp)

		// Deploy
		auth.POST("/apps/:appId/deploy", dv, middleware.RequireScope("deploy", "write"), h.DeployApp)
		auth.POST("/apps/:appId/rollback", dv, middleware.RequireScope("deploy", "write"), h.RollbackApp)
		auth.GET("/apps/:appId/deployments", middleware.RequireScope("read"), h.ListDeployments)
		auth.POST("/apps/:appId/restart", dv, middleware.RequireScope("deploy", "write"), h.RestartApp)
		auth.POST("/apps/:appId/scale", dv, middleware.RequireScope("deploy", "write"), h.ScaleApp)
		auth.POST("/apps/:appId/sleep", dv, middleware.RequireScope("deploy", "write"), h.SleepApp)
		auth.POST("/apps/:appId/wake", dv, middleware.RequireScope("deploy", "write"), h.WakeApp)
		auth.POST("/apps/:appId/stop", dv, middleware.RequireScope("deploy", "write"), h.StopApp)
		auth.POST("/apps/:appId/start", dv, middleware.RequireScope("deploy", "write"), h.StartApp)

		// Cronjob management
		auth.POST("/apps/:appId/cronjobs/:name/trigger", dv, middleware.RequireScope("deploy", "write"), h.TriggerCronJob)
		auth.GET("/apps/:appId/cronjobs/status", middleware.RequireScope("read"), h.GetCronJobStatuses)

		// Pod file browser (requires developer+ role — exec into pods can expose secrets)
		auth.GET("/apps/:appId/files", dv, middleware.RequireScope("read"), h.ListPodFiles)
		auth.GET("/apps/:appId/files/read", dv, middleware.RequireScope("read"), h.ReadPodFile)
		auth.POST("/apps/:appId/files/write", dv, middleware.RequireScope("write"), h.WritePodFile)

		// Rate limiting
		auth.GET("/apps/:appId/rate-limits", middleware.RequireScope("read"), h.GetRateLimits)
		auth.PUT("/apps/:appId/rate-limits", dv, middleware.RequireScope("write"), h.UpdateRateLimits)

		// Builds
		auth.POST("/apps/:appId/builds", dv, middleware.RequireScope("deploy", "write"), h.TriggerBuild)
		auth.GET("/apps/:appId/builds", middleware.RequireScope("read"), h.ListBuilds)
		auth.GET("/apps/:appId/builds/:buildId", middleware.RequireScope("read"), h.GetBuild)
		auth.GET("/apps/:appId/builds/:buildId/logs", middleware.RequireScope("read"), h.GetBuildLogs)
		auth.POST("/apps/:appId/builds/:buildId/cancel", dv, middleware.RequireScope("deploy", "write"), h.CancelBuild)

		// Environment Variables (per app per environment) -- non-secret config
		auth.POST("/apps/:appId/envs/:env/envvars", dv, h.CreateAppEnvVars)
		auth.GET("/apps/:appId/envs/:env/envvars", dv, h.ListAppEnvVars)
		auth.DELETE("/apps/:appId/envs/:env/envvars/:key", dv, h.DeleteAppEnvVarKey)

		// Secrets (per app per environment) -- viewers have no access
		auth.POST("/apps/:appId/envs/:env/secrets", dv, h.CreateAppEnvSecret)
		auth.GET("/apps/:appId/envs/:env/secrets", dv, h.ListAppEnvSecrets)
		auth.DELETE("/apps/:appId/envs/:env/secrets/:key", dv, h.DeleteAppEnvSecretKey)
		auth.GET("/apps/:appId/envs/:env/secrets/reveal", dv, h.RevealAppEnvSecretValues)
		auth.GET("/secrets", dv, h.ListSecrets)
		auth.GET("/secrets/:secretId/reveal", dv, h.RevealSecretValues)
		auth.PUT("/secrets/:secretId", dv, h.UpdateSecret)
		auth.DELETE("/secrets/:secretId", dv, h.DeleteSecret)
		auth.POST("/secrets/registry", dv, h.CreateRegistrySecret)
		auth.GET("/secrets/registry", dv, h.ListRegistrySecrets)
		auth.DELETE("/secrets/registry/:name", dv, h.DeleteRegistrySecret)

		// Shared Secrets (project-scoped, opt-in per app)
		auth.POST("/projects/:projectId/shared-secrets", dv, h.CreateSharedSecret)
		auth.GET("/projects/:projectId/shared-secrets", h.ListSharedSecrets)
		auth.PUT("/projects/:projectId/shared-secrets/:name", dv, h.UpdateSharedSecret)
		auth.GET("/projects/:projectId/shared-secrets/:name/reveal", middleware.RequireProjectRole(database, "owner"), h.RevealSharedSecret)
		auth.DELETE("/projects/:projectId/shared-secrets/:name", dv, h.DeleteSharedSecret)
		auth.POST("/apps/:appId/shared-secrets", dv, h.BindSharedSecret)
		auth.GET("/apps/:appId/shared-secrets", h.ListAppSharedSecrets)
		auth.DELETE("/apps/:appId/shared-secrets/:name", dv, h.UnbindSharedSecret)

		registerMFARoutes(auth, h)

		// Project transfer between Vesta instances. Export is gated like a secret
		// reveal because the bundle contains every secret in the project; import is
		// admin-only because it creates instance-level registry credentials.
		auth.GET("/instance/identity", h.GetInstanceIdentity)
		auth.POST("/projects/import", middleware.RequireRole("admin"), h.ImportProject)
		auth.POST("/projects/:projectId/export", dv, h.ExportProject)

		// Project Members (owner management)
		auth.GET("/projects/:projectId/members", h.ListProjectMembers)
		auth.POST("/projects/:projectId/members", middleware.RequireRole("admin"), h.AddProjectMember)
		auth.DELETE("/projects/:projectId/members/:userId", middleware.RequireRole("admin"), h.RemoveProjectMember)

		// Logs and monitoring
		auth.GET("/apps/:appId/diagnostics", middleware.RequireScope("read"), h.GetAppDiagnostics)
		auth.GET("/apps/:appId/logs", h.StreamLogs)
		auth.GET("/apps/:appId/logs/ws", h.StreamLogsWS)
		auth.GET("/apps/:appId/exec", dv, h.ExecWS)
		auth.GET("/apps/:appId/metrics", h.GetMetrics)
		auth.GET("/apps/:appId/metrics/prometheus", h.GetPrometheusMetrics)
		auth.GET("/metrics/prometheus/status", h.GetPrometheusStatus)

		// Templates
		auth.GET("/templates", h.ListTemplates)
		auth.POST("/templates/:id/deploy", dv, h.DeployTemplate)

		// Health Dashboard
		auth.GET("/health/dashboard", h.GetHealthDashboard)

		// Notifications -- viewers can see channels and history but not manage
		auth.POST("/projects/:projectId/notifications", dv, h.CreateNotificationChannel)
		auth.GET("/projects/:projectId/notifications", h.ListNotificationChannels)
		auth.PUT("/projects/:projectId/notifications/:channelId", dv, h.UpdateNotificationChannel)
		auth.DELETE("/projects/:projectId/notifications/:channelId", dv, h.DeleteNotificationChannel)
		auth.POST("/projects/:projectId/notifications/:channelId/test", dv, h.TestNotificationChannel)
		auth.GET("/projects/:projectId/notifications/history", h.ListNotificationHistory)

		// Alert rules
		auth.POST("/projects/:projectId/alerts", dv, h.CreateAlertRule)
		auth.GET("/projects/:projectId/alerts", h.ListAlertRules)
		auth.PUT("/projects/:projectId/alerts/:ruleId", dv, h.UpdateAlertRule)
		auth.DELETE("/projects/:projectId/alerts/:ruleId", dv, h.DeleteAlertRule)

		// Dependencies
		auth.GET("/projects/:projectId/dependencies", h.GetAppDependencies)

		// Scheduled deployments
		auth.POST("/projects/:projectId/scheduled-deployments", dv, h.CreateScheduledDeployment)
		auth.GET("/projects/:projectId/scheduled-deployments", h.ListScheduledDeployments)
		auth.DELETE("/projects/:projectId/scheduled-deployments/:deploymentId", dv, h.CancelScheduledDeployment)

		// API tokens
		auth.GET("/auth/tokens", h.ListAPITokens)
		auth.POST("/auth/tokens", h.CreateAPIToken)
		auth.DELETE("/auth/tokens/:id", h.RevokeAPIToken)

		// Audit log (admin only - it spans every project and records auth events).
		// /activity stays open to all roles but scopes its results per caller.
		auth.GET("/audit-logs", middleware.RequireRole("admin"), h.ListAuditLogs)
		auth.GET("/activity", h.GetActivityFeed)

		// Webhook delivery log (admin only)
		auth.GET("/webhook-deliveries", middleware.RequireRole("admin"), h.ListWebhookDeliveries)

		// GitHub App settings (admin only)
		auth.POST("/github/manifest", middleware.RequireRole("admin"), h.GetGitHubAppManifest)
		auth.GET("/settings/github-app", middleware.RequireRole("admin"), h.GetGitHubAppStatus)
		auth.GET("/settings/github-app/installations", middleware.RequireRole("admin"), h.ListGitHubAppInstallations)
		auth.DELETE("/settings/github-app", middleware.RequireRole("admin"), h.DeleteGitHubApp)

		// Git helpers
		auth.GET("/git/branches", dv, h.ListRepoBranches)
		auth.GET("/git/repos", dv, h.ListAccessibleRepos)
	}

	if err := verifyPartialAuthRoutes(r); err != nil {
		log.Fatal(err)
	}

	log.Printf("Vesta API server starting on :%s", port)

	// Start scheduled deployment worker
	scheduler := &services.ScheduledDeploymentWorker{DB: database, K8s: kc}
	go scheduler.Start(context.Background())

	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
