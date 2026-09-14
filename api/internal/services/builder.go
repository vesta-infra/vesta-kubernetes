package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/git"
)

const (
	BuildStrategyDockerfile = "dockerfile"
	BuildStrategyNixpacks   = "nixpacks"
	BuildStrategyBuildpacks = "buildpacks"

	buildNamespace = "vesta-system"
)

type BuildRequest struct {
	AppID          string
	ProjectID      string
	Environment    string
	Strategy       string
	Repository     string
	Provider       string // github | gitlab | bitbucket; empty means github
	Host           string // git server; empty means github.com
	Branch         string
	CommitSHA      string
	Dockerfile     string
	ImageDest      string // full destination image:tag
	RegistrySecret string // docker-registry secret name for push
	GitSecretName  string // optional: secret with git credentials
	TriggeredBy    string
}

type Builder struct {
	clientset kubernetes.Interface
	db        *db.DB
	notifier  *Notifier
	githubApp *GitHubAppService
	providers *git.Registry

	// connections resolves which connection serves a repository. A func rather than a
	// handle on the database, so the builder keeps no opinion about where connections are
	// stored and stays testable without one.
	connections func(context.Context, git.RepoRef) (git.Connection, error)
}

// SetProviders gives the builder the provider registry, so a clone credential comes with
// the username that provider expects instead of the GitHub one being assumed.
func (b *Builder) SetProviders(r *git.Registry) { b.providers = r }

func NewBuilder(clientset kubernetes.Interface, database *db.DB, notifier *Notifier) *Builder {
	return &Builder{
		clientset: clientset,
		db:        database,
		notifier:  notifier,
	}
}

// SetGitHubApp sets the GitHub App service for token generation.
func (b *Builder) SetGitHubApp(ghApp *GitHubAppService) {
	b.githubApp = ghApp
}

// TriggerBuild creates a Kubernetes Job that builds and pushes a container image,
// then records the build in the database. It returns the build ID.
func (b *Builder) TriggerBuild(ctx context.Context, req BuildRequest) (string, error) {
	if req.Strategy == "" {
		req.Strategy = BuildStrategyDockerfile
	}
	if req.Dockerfile == "" {
		req.Dockerfile = "Dockerfile"
	}

	shortSHA := req.CommitSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	if shortSHA == "" {
		shortSHA = fmt.Sprintf("%d", time.Now().Unix())
	}

	jobName := fmt.Sprintf("build-%s-%s", req.AppID, shortSHA)
	// K8s names must be <= 63 chars and lowercase DNS safe
	if len(jobName) > 63 {
		jobName = jobName[:63]
	}
	jobName = strings.ToLower(jobName)

	// If no git secret is set, ask the provider for one.
	//
	// The username used to be the literal "x-access-token", which is GitHub-App specific --
	// GitLab expects "oauth2" and Bitbucket "x-token-auth", and the wrong one fails the
	// clone with a bare 403. It now travels with the token.
	ephemeralSecret := ""
	if req.GitSecretName == "" && req.Repository != "" {
		cred, err := b.cloneCredential(ctx, req)
		if err == nil && !cred.Empty() {
			ephemeralSecret = fmt.Sprintf("git-token-%s", jobName)
			if len(ephemeralSecret) > 63 {
				ephemeralSecret = ephemeralSecret[:63]
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ephemeralSecret,
					Namespace: buildNamespace,
					Labels: map[string]string{
						"kubernetes.getvesta.sh/ephemeral": "true",
						"kubernetes.getvesta.sh/build":     jobName,
					},
				},
				Type: corev1.SecretTypeOpaque,
				StringData: map[string]string{
					"token":    cred.Token,
					"username": cred.Username,
				},
			}
			if _, err := b.clientset.CoreV1().Secrets(buildNamespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
				log.Printf("[builder] failed to create ephemeral git secret: %v", err)
			} else {
				req.GitSecretName = ephemeralSecret
			}
		}
	}

	// Record build in DB
	build := db.Build{
		AppID:       req.AppID,
		ProjectID:   req.ProjectID,
		Environment: req.Environment,
		Status:      "pending",
		Strategy:    req.Strategy,
		CommitSHA:   req.CommitSHA,
		Branch:      req.Branch,
		Repository:  req.Repository,
		Image:       req.ImageDest,
		JobName:     jobName,
		TriggeredBy: req.TriggeredBy,
	}

	buildID, err := b.db.InsertBuild(ctx, build)
	if err != nil {
		return "", fmt.Errorf("failed to record build: %w", err)
	}

	job, err := b.createBuildJob(req, jobName)
	if err != nil {
		b.db.UpdateBuildStatus(ctx, buildID, "failed", err.Error())
		return buildID, fmt.Errorf("failed to create build job spec: %w", err)
	}

	createdJob, err := b.clientset.BatchV1().Jobs(buildNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		b.db.UpdateBuildStatus(ctx, buildID, "failed", err.Error())
		if ephemeralSecret != "" {
			// The Job never existed, so nothing will ever collect the token.
			b.deleteEphemeralSecret(ctx, ephemeralSecret)
		}
		return buildID, fmt.Errorf("failed to create build job: %w", err)
	}

	// Hand the token secret to the Job so it is collected with it.
	//
	// The secret holds a live git credential and used to outlive every build: it had no
	// owner, nothing deleted it, and the Job's TTL only removed the Job. One permanent
	// Secret containing a usable token accumulated per build. The owner reference can only
	// be set now, because it needs the Job's UID.
	if ephemeralSecret != "" {
		b.adoptEphemeralSecret(ctx, ephemeralSecret, createdJob)
	}

	b.db.UpdateBuildStatus(ctx, buildID, "running", "")

	b.notifier.Send(ctx, NotificationEvent{
		Type:        EventBuildStarted,
		ProjectID:   req.ProjectID,
		AppID:       req.AppID,
		Environment: req.Environment,
		Image:       req.ImageDest,
		TriggeredBy: req.TriggeredBy,
		Message:     fmt.Sprintf("Build started for %s (%s) — %s", req.AppID, req.Strategy, shortSHA),
	})

	// Watch the job in the background
	go b.watchBuild(buildID, jobName, req, ephemeralSecret)

	return buildID, nil
}

func (b *Builder) createBuildJob(req BuildRequest, jobName string) (*batchv1.Job, error) {
	var container corev1.Container
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount

	// Docker config volume for registry push
	if req.RegistrySecret != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "docker-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: req.RegistrySecret,
					Items: []corev1.KeyToPath{
						{Key: ".dockerconfigjson", Path: "config.json"},
					},
				},
			},
		})
	}

	gitHost := req.Host
	if gitHost == "" {
		gitHost = "github.com"
	}

	switch req.Strategy {
	case BuildStrategyDockerfile:
		kanikoMountPath := "/kaniko/.docker"
		if req.RegistrySecret != "" {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "docker-config",
				MountPath: kanikoMountPath,
			})
		}

		gitContext := fmt.Sprintf("git://%s/%s.git#refs/heads/%s", gitHost, req.Repository, req.Branch)
		if req.CommitSHA != "" {
			gitContext = fmt.Sprintf("git://%s/%s.git#%s", gitHost, req.Repository, req.CommitSHA)
		}

		args := []string{
			"--dockerfile=" + req.Dockerfile,
			"--context=" + gitContext,
			"--destination=" + req.ImageDest,
			"--cache=true",
			"--snapshot-mode=redo",
		}

		container = corev1.Container{
			Name:         "build",
			Image:        "gcr.io/kaniko-project/executor:latest",
			Args:         args,
			VolumeMounts: volumeMounts,
		}

		// If we have git credentials, pass as env
		if req.GitSecretName != "" {
			container.Env = append(container.Env, corev1.EnvVar{
				Name: "GIT_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
						Key:                  "token",
					},
				},
			})
			// Kaniko uses GIT_USERNAME/GIT_PASSWORD
			container.Env = append(container.Env,
				corev1.EnvVar{
					Name: "GIT_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
							Key:                  "username",
							Optional:             boolPtr(true),
						},
					},
				},
				corev1.EnvVar{
					Name: "GIT_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
							Key:                  "token",
						},
					},
				},
			)
		}

	case BuildStrategyNixpacks:
		workspaceVolume := corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
		volumes = append(volumes, workspaceVolume)

		nixMounts := []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
		}
		if req.RegistrySecret != "" {
			nixMounts = append(nixMounts, corev1.VolumeMount{
				Name:      "docker-config",
				MountPath: "/root/.docker",
			})
		}

		script := nixpacksScript

		container = corev1.Container{
			Name:         "build",
			Image:        "ghcr.io/railwayapp/nixpacks:latest",
			Command:      []string{"/bin/sh", "-c"},
			Args:         []string{script},
			VolumeMounts: nixMounts,
		}

		// Everything the script reads arrives as an environment variable, so the script
		// itself stays a constant and a branch name cannot become shell.
		container.Env = append(container.Env, buildScriptEnv(req)...)

		if req.GitSecretName != "" {
			container.Env = append(container.Env,
				corev1.EnvVar{
					Name: "GIT_TOKEN",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
							Key:                  "token",
						},
					},
				},
				corev1.EnvVar{
					Name: "GIT_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
							Key:                  "username",
							Optional:             boolPtr(true),
						},
					},
				},
			)
		}

	case BuildStrategyBuildpacks:
		workspaceVolume := corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
		volumes = append(volumes, workspaceVolume)

		bpMounts := []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
		}
		if req.RegistrySecret != "" {
			bpMounts = append(bpMounts, corev1.VolumeMount{
				Name:      "docker-config",
				MountPath: "/home/cnb/.docker",
			})
		}

		script := buildpacksScript

		container = corev1.Container{
			Name:         "build",
			Image:        "gcr.io/buildpacks/builder:google-22",
			Command:      []string{"/bin/sh", "-c"},
			Args:         []string{script},
			VolumeMounts: bpMounts,
		}

		// Everything the script reads arrives as an environment variable, so the script
		// itself stays a constant and a branch name cannot become shell.
		container.Env = append(container.Env, buildScriptEnv(req)...)

		if req.GitSecretName != "" {
			container.Env = append(container.Env,
				corev1.EnvVar{
					Name: "GIT_TOKEN",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
							Key:                  "token",
						},
					},
				},
				corev1.EnvVar{
					Name: "GIT_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: req.GitSecretName},
							Key:                  "username",
							Optional:             boolPtr(true),
						},
					},
				},
			)
		}

	default:
		return nil, fmt.Errorf(
			"build strategy %q is not implemented; use one of %s",
			req.Strategy, strings.Join(SupportedBuildStrategies, ", "))
	}

	backoffLimit := int32(0)
	ttl := int32(3600) // clean up completed jobs after 1 hour

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: buildNamespace,
			Labels: map[string]string{
				"kubernetes.getvesta.sh/build":   "true",
				"kubernetes.getvesta.sh/app":     req.AppID,
				"kubernetes.getvesta.sh/project": req.ProjectID,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kubernetes.getvesta.sh/build":   "true",
						"kubernetes.getvesta.sh/app":     req.AppID,
						"kubernetes.getvesta.sh/project": req.ProjectID,
						"job-name":                       jobName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{container},
					Volumes:       volumes,
				},
			},
		},
	}

	return job, nil
}

// watchBuild polls the Job status and updates the DB + triggers deploy on success.
func (b *Builder) watchBuild(buildID, jobName string, req BuildRequest, ephemeralSecret string) {
	ctx := context.Background()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Drop the git token as soon as the build stops needing it, rather than leaving it
	// until the Job's hour-long TTL expires. The owner reference set at creation is the
	// backstop for the case this defer cannot cover: if the API restarts, this goroutine
	// dies with it and nothing here runs at all.
	if ephemeralSecret != "" {
		defer b.deleteEphemeralSecret(ctx, ephemeralSecret)
	}

	timeout := time.After(30 * time.Minute)

	for {
		select {
		case <-timeout:
			b.db.UpdateBuildStatus(ctx, buildID, "failed", "build timed out after 30 minutes")
			b.notifier.Send(ctx, NotificationEvent{
				Type:      EventBuildFailed,
				ProjectID: req.ProjectID,
				AppID:     req.AppID,
				Message:   fmt.Sprintf("Build timed out for %s", req.AppID),
			})
			b.reportStatus(ctx, req, git.StateError, "Vesta build timed out after 30 minutes")
			return

		case <-ticker.C:
			job, err := b.clientset.BatchV1().Jobs(buildNamespace).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				log.Printf("[builder] error checking job %s: %v", jobName, err)
				continue
			}

			for _, cond := range job.Status.Conditions {
				if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
					b.db.UpdateBuildStatus(ctx, buildID, "success", "")
					b.notifier.Send(ctx, NotificationEvent{
						Type:        EventBuildSucceeded,
						ProjectID:   req.ProjectID,
						AppID:       req.AppID,
						Environment: req.Environment,
						Image:       req.ImageDest,
						Message:     fmt.Sprintf("Build succeeded for %s — deploying %s", req.AppID, req.ImageDest),
					})
					// Auto-deploy: update the VestaApp image tag
					b.onBuildSuccess(ctx, req)
					b.reportStatus(ctx, req, git.StateSuccess, "Vesta build succeeded")
					return
				}

				if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
					errMsg := cond.Message
					if errMsg == "" {
						errMsg = "build job failed"
					}
					b.db.UpdateBuildStatus(ctx, buildID, "failed", errMsg)
					b.notifier.Send(ctx, NotificationEvent{
						Type:        EventBuildFailed,
						ProjectID:   req.ProjectID,
						AppID:       req.AppID,
						Environment: req.Environment,
						Message:     fmt.Sprintf("Build failed for %s: %s", req.AppID, errMsg),
					})
					b.reportStatus(ctx, req, git.StateFailed, truncateForStatus(errMsg))
					return
				}
			}
		}
	}
}

// onBuildSuccess patches the VestaApp CRD to trigger the operator to deploy the new image.
func (b *Builder) onBuildSuccess(ctx context.Context, req BuildRequest) {
	// Extract the tag from the full image destination
	parts := strings.SplitN(req.ImageDest, ":", 2)
	if len(parts) != 2 {
		log.Printf("[builder] cannot parse image tag from %s", req.ImageDest)
		return
	}
	tag := parts[1]

	if err := b.RecordCommitSHA(ctx, req.AppID, req.CommitSHA); err != nil {
		log.Printf("[builder] failed to record commit SHA on VestaApp %s: %v", req.AppID, err)
	}

	if err := b.PinEnvironmentTag(ctx, req.AppID, req.Environment, tag); err != nil {
		log.Printf("[builder] failed to update VestaApp %s after build: %v", req.AppID, err)
	}
}

// PinEnvironmentTag points one environment at an image tag.
//
// Shared with the webhook's pre-built-image path so both roll out the same way. The rule
// that matters is the one encoded below: spec.image.tag is only the default for
// environments that carry no tag of their own, so writing it would roll this image out to
// every other such environment rather than the one being deployed.
func (b *Builder) PinEnvironmentTag(ctx context.Context, appID, environment, tag string) error {
	appPath := fmt.Sprintf("/apis/kubernetes.getvesta.sh/v1alpha1/namespaces/%s/vestaapps/%s", buildNamespace, appID)

	envIndex, envImage, err := b.environmentImage(ctx, appPath, environment)
	if err != nil {
		return fmt.Errorf("resolve environment %q: %w", environment, err)
	}

	var patchType types.PatchType
	var patch []byte
	if envIndex >= 0 {
		// Preserve any other per-environment image fields (repository, pullPolicy…).
		envImage["tag"] = tag
		value, mErr := json.Marshal(envImage)
		if mErr != nil {
			return fmt.Errorf("encode image config: %w", mErr)
		}
		patchType = types.JSONPatchType
		patch = []byte(fmt.Sprintf(`[{"op":"add","path":"/spec/environments/%d/image","value":%s}]`, envIndex, value))
	} else {
		// App declares no environments — the global tag is the only target.
		patchType = types.MergePatchType
		patch = []byte(fmt.Sprintf(`{"spec":{"image":{"tag":"%s"}}}`, tag))
	}

	if _, err := b.clientset.Discovery().RESTClient().
		Patch(patchType).
		AbsPath(appPath).
		Body(patch).
		DoRaw(ctx); err != nil {
		return err
	}
	return nil
}

// RecordCommitSHA notes which commit an app was last rolled out from.
//
// This writes status.lastCommitSHA, through the status subresource. It used to write
// spec.git.commitSHA, a field the CRD does not declare -- so the API server pruned it on
// every write and nothing was ever recorded. The commit is observed state, not something
// anyone asked for, so status is where it belongs; and status can only be written through
// its own subresource, which a patch to the main resource silently drops.
func (b *Builder) RecordCommitSHA(ctx context.Context, appID, commitSHA string) error {
	if commitSHA == "" {
		return nil
	}
	statusPath := fmt.Sprintf("/apis/kubernetes.getvesta.sh/v1alpha1/namespaces/%s/vestaapps/%s/status", buildNamespace, appID)
	patch := fmt.Sprintf(`{"status":{"lastCommitSHA":%q}}`, commitSHA)

	_, err := b.clientset.Discovery().RESTClient().
		Patch(types.MergePatchType).
		AbsPath(statusPath).
		Body([]byte(patch)).
		DoRaw(ctx)
	return err
}

// environmentImage returns the index of envName in spec.environments along with its
// current image config, or -1 if the app declares no environments at all. An app
// that declares environments but not envName is an error — falling back to the
// global tag there would deploy the build to every environment.
func (b *Builder) environmentImage(ctx context.Context, appPath, envName string) (int, map[string]interface{}, error) {
	raw, err := b.clientset.Discovery().RESTClient().
		Get().
		AbsPath(appPath).
		DoRaw(ctx)
	if err != nil {
		return -1, nil, fmt.Errorf("get vestaapp: %w", err)
	}

	var app struct {
		Spec struct {
			Environments []struct {
				Name  string                 `json:"name"`
				Image map[string]interface{} `json:"image"`
			} `json:"environments"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &app); err != nil {
		return -1, nil, fmt.Errorf("decode vestaapp: %w", err)
	}

	if len(app.Spec.Environments) == 0 {
		return -1, nil, nil
	}
	for i, env := range app.Spec.Environments {
		if env.Name == envName {
			image := env.Image
			if image == nil {
				image = map[string]interface{}{}
			}
			return i, image, nil
		}
	}
	return -1, nil, fmt.Errorf("environment %q not declared on app", envName)
}

func boolPtr(v bool) *bool { return &v }

// adoptEphemeralSecret makes a build Job the owner of its git-token Secret, so Kubernetes
// deletes the token when it deletes the Job. Best effort: a build that runs is worth more
// than a tidy namespace, and watchBuild deletes the secret directly when the build ends.
func (b *Builder) adoptEphemeralSecret(ctx context.Context, name string, job *batchv1.Job) {
	patch := fmt.Sprintf(
		`{"metadata":{"ownerReferences":[{"apiVersion":"batch/v1","kind":"Job","name":%q,"uid":%q,"controller":true,"blockOwnerDeletion":false}]}}`,
		job.Name, job.UID)

	if _, err := b.clientset.CoreV1().Secrets(buildNamespace).
		Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		log.Printf("[builder] could not attach ephemeral secret %s to job %s: %v", name, job.Name, err)
	}
}

func (b *Builder) deleteEphemeralSecret(ctx context.Context, name string) {
	if err := b.clientset.CoreV1().Secrets(buildNamespace).
		Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		log.Printf("[builder] could not delete ephemeral secret %s: %v", name, err)
	}
}

// cloneCredential resolves the credential a build should clone with.
//
// Falls back to the GitHub App directly when no registry is wired, so a Builder constructed
// without one behaves as it did before rather than silently losing authentication.
func (b *Builder) cloneCredential(ctx context.Context, req BuildRequest) (git.Credential, error) {
	provider := req.Provider
	if provider == "" {
		provider = git.ProviderGitHub
	}

	ref, err := git.ParseRepoRef(provider, req.Host, req.Repository)
	if err != nil {
		return git.Credential{}, err
	}

	if p, ok := b.providers.Get(provider); ok {
		return p.CredentialFor(ctx, git.Connection{Provider: provider, Host: ref.Host}, ref)
	}

	if b.githubApp != nil && b.githubApp.IsConfigured() && provider == git.ProviderGitHub {
		token, err := b.githubApp.GetTokenForRepo(ctx, ref.Path)
		if err != nil {
			return git.Credential{}, err
		}
		return githubCredential(token), nil
	}
	return git.Credential{}, fmt.Errorf("no provider for %s", provider)
}

// reportStatus tells the git host how a build ended.
//
// Nothing did this before. A build start posted "pending" from the webhook handler and no
// code path ever posted anything else, so every commit Vesta built was left with a
// permanently pending check -- which blocks merges on any repository with a required status
// and looks, from the pull request, exactly like a build that never finished.
//
// Best effort on purpose: a host that cannot be told is not a reason to fail a build that
// already succeeded.
func (b *Builder) reportStatus(ctx context.Context, req BuildRequest, state git.BuildState, description string) {
	if req.CommitSHA == "" {
		return // a manual build with no commit has nothing to report against
	}

	provider := req.Provider
	if provider == "" {
		provider = git.ProviderGitHub
	}
	impl, ok := b.providers.Get(provider)
	if !ok {
		return
	}

	ref, err := git.ParseRepoRef(provider, req.Host, req.Repository)
	if err != nil {
		return
	}

	conn := git.Connection{Provider: ref.Provider, Host: ref.Host}
	if b.connections != nil {
		if resolved, err := b.connections(ctx, ref); err == nil {
			conn = resolved
		}
	}

	if err := impl.ReportStatus(ctx, conn, ref, req.CommitSHA, state, "", description); err != nil {
		log.Printf("[builder] could not report %s for %s: %v", state, req.AppID, err)
	}
}

// SetConnectionResolver gives the builder a way to find the connection serving a repository,
// so a status is reported through the credentials that repository actually belongs to.
func (b *Builder) SetConnectionResolver(fn func(context.Context, git.RepoRef) (git.Connection, error)) {
	b.connections = fn
}

// truncateForStatus keeps a build error short enough for a commit status description, which
// every provider caps well below the length a Kubernetes failure message can reach.
func truncateForStatus(msg string) string {
	const max = 140
	if len(msg) <= max {
		return msg
	}
	return msg[:max-1] + "\u2026"
}

// SupportedBuildStrategies is what TriggerBuild can actually build.
//
// Deliberately narrower than the CRD's enum, which also accepts "runpacks" and "image".
// "image" never reaches a builder -- an app deploying a pre-built image does not build --
// but "runpacks" is a genuine gap: it is accepted at admission and then fails at build time,
// which is the worst place to discover it.
//
// Removing it from the enum would be the obvious fix and is the wrong one. Narrowing a CRD
// schema is a data migration: an app already storing that value would become unappliable,
// and check-crd-compat.sh guards `required` fields but not enum narrowing, so nothing would
// catch the breakage. So the value stays accepted and the failure is at least explicit about
// what to use instead.
var SupportedBuildStrategies = []string{
	BuildStrategyDockerfile,
	BuildStrategyNixpacks,
	BuildStrategyBuildpacks,
}
