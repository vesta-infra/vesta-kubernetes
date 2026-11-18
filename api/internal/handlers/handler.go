package handlers

import (
	"context"
	"os"

	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/git"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/registry"
	"kubernetes.getvesta.sh/api/internal/scope"
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

	// GitProviders resolves spec.git.provider to an implementation. Before it existed the
	// field was written by the UI and read by nothing, so choosing GitLab silently behaved
	// as GitHub.
	GitProviders *git.Registry

	// Registry browses container registries so image repositories and tags can be picked
	// rather than typed.
	Registry *registry.Client

	// Scope resolves an app to its project, which almost every route needs and no route
	// carries: the project lives inside the app's custom resource.
	Scope *scope.Resolver

	// Sleep is the inactivity sweeper, told when an app is woken by hand so it honours the
	// minimum-awake floor rather than sleeping it again on the next pass.
	Sleep *services.SleepSweeper

	CertProvider *services.CertProviderService
	Updater      *services.Updater
}

func New(kc *k8s.Client, database *db.DB, notifier *services.Notifier) *Handler {
	builder := services.NewBuilder(kc.Clientset, database, notifier)
	ghNotifier := services.NewGitHubStatusNotifier()
	ghApp := services.NewGitHubAppService(kc.Clientset)
	builder.SetGitHubApp(ghApp)

	// One registry, shared by the handlers and the builder, so a repository resolves to the
	// same implementation wherever it is looked up. GitLab and Bitbucket join this list;
	// nothing else has to change to find them.
	providers := git.NewRegistry(
		services.NewGitHubProvider(ghApp, ghNotifier),
		services.NewGitLabProvider(kc.Clientset),
		services.NewBitbucketProvider(kc.Clientset),
	)
	builder.SetProviders(providers)
	// cert-manager resolves a ClusterIssuer's secretRefs against its own controller
	// namespace, so credential Secrets must be written there rather than in vesta-system.
	certProvider := services.NewCertProviderService(kc.Clientset, os.Getenv("CERT_MANAGER_NAMESPACE"))
	// Upgrades Vesta itself; carries the running version so an upgrade record knows what
	// it upgraded from, which the new process cannot work out after the fact.
	updater := services.NewUpdater(kc.Clientset, database, version.Version)
	h := &Handler{
		K8s: kc, DB: database, Notifier: notifier, Builder: builder,
		GitHubNotifier: ghNotifier, GitHubApp: ghApp, CertProvider: certProvider,
		Updater: updater, GitProviders: providers, Registry: registry.NewClient(),
	}

	// The builder reports build outcomes back to the git host, and needs the connection
	// serving a repository to do it with the right credentials. Wired after the Handler
	// exists because the lookup lives on it.
	builder.SetConnectionResolver(func(ctx context.Context, ref git.RepoRef) (git.Connection, error) {
		return h.connectionFor(ctx, ref, ""), nil
	})

	// The lookup is a closure rather than a client handle so the cache stays testable with
	// no cluster.
	h.Scope = scope.NewResolver(func(ctx context.Context, appID string) (scope.Scope, error) {
		obj, err := kc.GetResource(ctx, k8s.VestaAppGVR, vestaSystemNS, appID)
		if err != nil {
			return scope.Scope{}, err
		}
		spec, _, _ := unstructuredNestedMap(obj.Object, "spec")

		out := scope.Scope{Project: getNestedString(spec, "project")}
		if envs, ok := spec["environments"].([]interface{}); ok {
			for _, e := range envs {
				if m, ok := e.(map[string]interface{}); ok {
					if name := getNestedString(m, "name"); name != "" {
						out.Environments = append(out.Environments, name)
					}
				}
			}
		}
		return out, nil
	})

	return h
}
