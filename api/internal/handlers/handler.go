package handlers

import (
	"os"

	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/services"
	"kubernetes.getvesta.sh/api/internal/version"
)

type Handler struct {
	K8s            *k8s.Client
	DB             *db.DB
	Notifier       *services.Notifier
	Builder        *services.Builder
	GitHubNotifier *services.GitHubStatusNotifier
	GitHubApp      *services.GitHubAppService
	CertProvider   *services.CertProviderService
	Updater        *services.Updater
}

func New(kc *k8s.Client, database *db.DB, notifier *services.Notifier) *Handler {
	builder := services.NewBuilder(kc.Clientset, database, notifier)
	ghNotifier := services.NewGitHubStatusNotifier()
	ghApp := services.NewGitHubAppService(kc.Clientset)
	builder.SetGitHubApp(ghApp)
	// cert-manager resolves a ClusterIssuer's secretRefs against its own controller
	// namespace, so credential Secrets must be written there rather than in vesta-system.
	certProvider := services.NewCertProviderService(kc.Clientset, os.Getenv("CERT_MANAGER_NAMESPACE"))
	// Upgrades Vesta itself; carries the running version so an upgrade record knows what
	// it upgraded from, which the new process cannot work out after the fact.
	updater := services.NewUpdater(kc.Clientset, database, version.Version)
	return &Handler{
		K8s: kc, DB: database, Notifier: notifier, Builder: builder,
		GitHubNotifier: ghNotifier, GitHubApp: ghApp, CertProvider: certProvider,
		Updater: updater,
	}
}
