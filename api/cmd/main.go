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

	// Authenticated routes
	auth := v1.Group("")
	auth.Use(middleware.AuthRequired(database))
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
		auth.POST("/projects", h.CreateProject)
		auth.GET("/projects", h.ListProjects)
		auth.GET("/projects/:projectId", h.GetProject)
		auth.PUT("/projects/:projectId", h.UpdateProject)
		auth.DELETE("/projects/:projectId", h.DeleteProject)

		// Environments
		auth.POST("/projects/:projectId/environments", h.CreateEnvironment)
		auth.GET("/projects/:projectId/environments", h.ListEnvironments)
		auth.DELETE("/projects/:projectId/environments/:env", h.DeleteEnvironment)

		// Apps
		auth.GET("/pod-sizes", h.ListPodSizes)
		auth.POST("/projects/:projectId/apps", middleware.RequireScope("write"), h.CreateApp)
		auth.GET("/projects/:projectId/apps", middleware.RequireScope("read"), h.ListProjectApps)
		auth.GET("/apps", middleware.RequireScope("read"), h.ListApps)
		auth.GET("/apps/:appId", middleware.RequireScope("read"), h.GetApp)
		auth.PUT("/apps/:appId", middleware.RequireScope("write"), h.UpdateApp)
		auth.DELETE("/apps/:appId", middleware.RequireScope("write"), h.DeleteApp)

		// Deploy
		auth.POST("/apps/:appId/deploy", middleware.RequireScope("deploy", "write"), h.DeployApp)
		auth.POST("/apps/:appId/rollback", middleware.RequireScope("deploy", "write"), h.RollbackApp)
		auth.GET("/apps/:appId/deployments", middleware.RequireScope("read"), h.ListDeployments)
		auth.POST("/apps/:appId/restart", middleware.RequireScope("deploy", "write"), h.RestartApp)
		auth.POST("/apps/:appId/scale", middleware.RequireScope("deploy", "write"), h.ScaleApp)

		// Secrets (per app per environment)
		auth.POST("/apps/:appId/envs/:env/secrets", h.CreateAppEnvSecret)
		auth.GET("/apps/:appId/envs/:env/secrets", h.ListAppEnvSecrets)
		auth.DELETE("/apps/:appId/envs/:env/secrets/:key", h.DeleteAppEnvSecretKey)
		auth.GET("/secrets", h.ListSecrets)
		auth.PUT("/secrets/:secretId", h.UpdateSecret)
		auth.DELETE("/secrets/:secretId", h.DeleteSecret)
		auth.POST("/secrets/registry", h.CreateRegistrySecret)
		auth.GET("/secrets/registry", h.ListRegistrySecrets)
		auth.DELETE("/secrets/registry/:name", h.DeleteRegistrySecret)

		// Logs and monitoring
		auth.GET("/apps/:appId/logs", h.StreamLogs)
		auth.GET("/apps/:appId/metrics", h.GetMetrics)

		// Templates
		auth.GET("/templates", h.ListTemplates)
		auth.POST("/templates/:id/deploy", h.DeployTemplate)

		// Notifications
		auth.POST("/projects/:projectId/notifications", h.CreateNotificationChannel)
		auth.GET("/projects/:projectId/notifications", h.ListNotificationChannels)
		auth.PUT("/projects/:projectId/notifications/:channelId", h.UpdateNotificationChannel)
		auth.DELETE("/projects/:projectId/notifications/:channelId", h.DeleteNotificationChannel)
		auth.POST("/projects/:projectId/notifications/:channelId/test", h.TestNotificationChannel)
		auth.GET("/projects/:projectId/notifications/history", h.ListNotificationHistory)

		// API tokens
		auth.GET("/auth/tokens", h.ListAPITokens)
		auth.POST("/auth/tokens", h.CreateAPIToken)
		auth.DELETE("/auth/tokens/:id", h.RevokeAPIToken)
	}

	log.Printf("Vesta API server starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
