package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"kubernetes.getvesta.sh/api/internal/db"
)

const (
	BuildStrategyDockerfile = "dockerfile"
	BuildStrategyNixpacks   = "nixpacks"
	BuildStrategyBuildpacks  = "buildpacks"

	buildNamespace = "vesta-system"
)

type BuildRequest struct {
	AppID        string
	ProjectID    string
	Environment  string
	Strategy     string
	Repository   string
	Branch       string
	CommitSHA    string
	Dockerfile   string
	ImageDest    string // full destination image:tag
	RegistrySecret string // docker-registry secret name for push
	GitSecretName  string // optional: secret with git credentials
	TriggeredBy  string
}

type Builder struct {
	clientset kubernetes.Interface
	db        *db.DB
	notifier  *Notifier
}

func NewBuilder(clientset kubernetes.Interface, database *db.DB, notifier *Notifier) *Builder {
	return &Builder{
		clientset: clientset,
		db:        database,
		notifier:  notifier,
	}
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

	_, err = b.clientset.BatchV1().Jobs(buildNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		b.db.UpdateBuildStatus(ctx, buildID, "failed", err.Error())
		return buildID, fmt.Errorf("failed to create build job: %w", err)
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
	go b.watchBuild(buildID, jobName, req)

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

	gitURL := fmt.Sprintf("https://github.com/%s.git", req.Repository)

	switch req.Strategy {
	case BuildStrategyDockerfile:
		kanikoMountPath := "/kaniko/.docker"
		if req.RegistrySecret != "" {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "docker-config",
				MountPath: kanikoMountPath,
			})
		}

		gitContext := fmt.Sprintf("git://%s#refs/heads/%s", gitURL, req.Branch)
		if req.CommitSHA != "" {
			gitContext = fmt.Sprintf("git://%s#%s", gitURL, req.CommitSHA)
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

		cloneCmd := fmt.Sprintf("git clone --depth=1 -b %s %s /workspace", req.Branch, gitURL)
		if req.CommitSHA != "" {
			cloneCmd = fmt.Sprintf("git clone %s /workspace && cd /workspace && git checkout %s", gitURL, req.CommitSHA)
		}

		script := fmt.Sprintf(`set -e
%s
cd /workspace
nixpacks build . --name %s
# Push image using crane
crane push %s %s
echo "Build and push complete"
`, cloneCmd, req.ImageDest, req.ImageDest, req.ImageDest)

		container = corev1.Container{
			Name:         "build",
			Image:        "ghcr.io/railwayapp/nixpacks:latest",
			Command:      []string{"/bin/sh", "-c"},
			Args:         []string{script},
			VolumeMounts: nixMounts,
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

		cloneCmd := fmt.Sprintf("git clone --depth=1 -b %s %s /workspace", req.Branch, gitURL)
		if req.CommitSHA != "" {
			cloneCmd = fmt.Sprintf("git clone %s /workspace && cd /workspace && git checkout %s", gitURL, req.CommitSHA)
		}

		script := fmt.Sprintf(`set -e
%s
/cnb/lifecycle/creator -app=/workspace -run-image=gcr.io/buildpacks/gcp/run:v1 %s
echo "Build and push complete"
`, cloneCmd, req.ImageDest)

		container = corev1.Container{
			Name:         "build",
			Image:        "gcr.io/buildpacks/builder:google-22",
			Command:      []string{"/bin/sh", "-c"},
			Args:         []string{script},
			VolumeMounts: bpMounts,
		}

	default:
		return nil, fmt.Errorf("unsupported build strategy: %s", req.Strategy)
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
func (b *Builder) watchBuild(buildID, jobName string, req BuildRequest) {
	ctx := context.Background()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

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

	patch := fmt.Sprintf(`{"spec":{"image":{"tag":"%s"},"git":{"commitSHA":"%s"}}}`, tag, req.CommitSHA)
	_, err := b.clientset.Discovery().RESTClient().
		Patch("application/merge-patch+json").
		AbsPath(fmt.Sprintf("/apis/kubernetes.getvesta.sh/v1alpha1/namespaces/%s/vestaapps/%s", buildNamespace, req.AppID)).
		Body([]byte(patch)).
		DoRaw(ctx)
	if err != nil {
		log.Printf("[builder] failed to update VestaApp %s after build: %v", req.AppID, err)
	}
}

func boolPtr(v bool) *bool { return &v }
