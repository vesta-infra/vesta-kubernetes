package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/handlers"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/middleware"
	"kubernetes.getvesta.sh/api/internal/services"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
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

	// Webhooks (unauthenticated, verified by signature)
	v1.POST("/webhooks/:provider", h.ReceiveWebhook)

	// GitHub App manifest flow (callback is unauthenticated, state-verified)
	v1.GET("/github/callback", h.GitHubAppCallback)

	// Authenticated routes
	auth := v1.Group("")
	auth.Use(middleware.AuthRequired(database))
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

		// Builds
		auth.POST("/apps/:appId/builds", dv, middleware.RequireScope("deploy", "write"), h.TriggerBuild)
		auth.GET("/apps/:appId/builds", middleware.RequireScope("read"), h.ListBuilds)
		auth.GET("/apps/:appId/builds/:buildId", middleware.RequireScope("read"), h.GetBuild)
		auth.GET("/apps/:appId/builds/:buildId/logs", middleware.RequireScope("read"), h.GetBuildLogs)
		auth.POST("/apps/:appId/builds/:buildId/cancel", dv, middleware.RequireScope("deploy", "write"), h.CancelBuild)

		// Secrets (per app per environment) -- viewers have no access
		auth.POST("/apps/:appId/envs/:env/secrets", dv, h.CreateAppEnvSecret)
		auth.GET("/apps/:appId/envs/:env/secrets", dv, h.ListAppEnvSecrets)
		auth.DELETE("/apps/:appId/envs/:env/secrets/:key", dv, h.DeleteAppEnvSecretKey)
		auth.GET("/apps/:appId/envs/:env/secrets/reveal", middleware.RequireRole("admin"), h.RevealAppEnvSecretValues)
		auth.GET("/secrets", dv, h.ListSecrets)
		auth.GET("/secrets/:secretId/reveal", middleware.RequireRole("admin"), h.RevealSecretValues)
		auth.PUT("/secrets/:secretId", dv, h.UpdateSecret)
		auth.DELETE("/secrets/:secretId", dv, h.DeleteSecret)
		auth.POST("/secrets/registry", dv, h.CreateRegistrySecret)
		auth.GET("/secrets/registry", dv, h.ListRegistrySecrets)
		auth.DELETE("/secrets/registry/:name", dv, h.DeleteRegistrySecret)

		// Shared Secrets (project-scoped, opt-in per app)
		auth.POST("/projects/:projectId/shared-secrets", dv, h.CreateSharedSecret)
		auth.GET("/projects/:projectId/shared-secrets", h.ListSharedSecrets)
		auth.PUT("/projects/:projectId/shared-secrets/:name", dv, h.UpdateSharedSecret)
		auth.GET("/projects/:projectId/shared-secrets/:name/reveal", middleware.RequireRole("admin"), h.RevealSharedSecret)
		auth.DELETE("/projects/:projectId/shared-secrets/:name", dv, h.DeleteSharedSecret)
		auth.POST("/apps/:appId/shared-secrets", dv, h.BindSharedSecret)
		auth.GET("/apps/:appId/shared-secrets", h.ListAppSharedSecrets)
		auth.DELETE("/apps/:appId/shared-secrets/:name", dv, h.UnbindSharedSecret)

		// Logs and monitoring
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

		// API tokens
		auth.GET("/auth/tokens", h.ListAPITokens)
		auth.POST("/auth/tokens", h.CreateAPIToken)
		auth.DELETE("/auth/tokens/:id", h.RevokeAPIToken)

		// Audit log
		auth.GET("/audit-logs", h.ListAuditLogs)
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

	log.Printf("Vesta API server starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
