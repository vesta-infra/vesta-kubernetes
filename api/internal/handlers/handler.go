package handlers

import (
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/services"
)

type Handler struct {
	K8s            *k8s.Client
	DB             *db.DB
	Notifier       *services.Notifier
	Builder        *services.Builder
	GitHubNotifier *services.GitHubStatusNotifier
	GitHubApp      *services.GitHubAppService
}

func New(kc *k8s.Client, database *db.DB, notifier *services.Notifier) *Handler {
	builder := services.NewBuilder(kc.Clientset, database, notifier)
	ghNotifier := services.NewGitHubStatusNotifier()
	ghApp := services.NewGitHubAppService(kc.Clientset)
	builder.SetGitHubApp(ghApp)
	return &Handler{K8s: kc, DB: database, Notifier: notifier, Builder: builder, GitHubNotifier: ghNotifier, GitHubApp: ghApp}
}
