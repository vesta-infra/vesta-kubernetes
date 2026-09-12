package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"kubernetes.getvesta.sh/api/internal/db"
)

const (
	// chartRef is fixed, never caller-supplied. The version is the only thing an admin
	// chooses; letting them name the chart would turn this endpoint into "run any helm
	// chart as cluster-admin".
	chartRef = "oci://ghcr.io/vesta-infra/charts/vesta"

	// updateJobTimeout bounds the Job. Long enough for image pulls on a slow network,
	// short enough that a wedged upgrade eventually reports failure instead of hanging.
	updateJobTimeout = 20 * time.Minute
)

// UpdateRequest describes an upgrade of Vesta itself.
type UpdateRequest struct {
	Version     string
	Namespace   string
	TriggeredBy string
}

// Updater runs `helm upgrade` in a Job.
type Updater struct {
	Clientset   kubernetes.Interface
	DB          *db.DB
	FromVersion string
}

func NewUpdater(cs kubernetes.Interface, database *db.DB, fromVersion string) *Updater {
	return &Updater{Clientset: cs, DB: database, FromVersion: fromVersion}
}

// helmImage is overridable so an air-gapped install can mirror it.
func helmImage() string {
	if v := strings.TrimSpace(os.Getenv("VESTA_HELM_IMAGE")); v != "" {
		return v
	}
	return "alpine/helm:3.16.3"
}

// releaseName is the Helm release to upgrade. Discovered from the environment because the
// deployment names are "<release>-api" and an installation named something other than
// "vesta" would otherwise be upgraded under the wrong release, creating a second one.
func releaseName() string {
	if v := strings.TrimSpace(os.Getenv("VESTA_RELEASE_NAME")); v != "" {
		return v
	}
	return "vesta"
}

// Start creates the upgrade Job and returns its name.
//
// Nothing here waits for the result. The upgrade replaces the API pod running this code,
// so the process that started it will usually be gone before the Job finishes; the
// watcher below reconciles the outcome after the new process comes up.
func (u *Updater) Start(ctx context.Context, req UpdateRequest) (string, error) {
	if running, err := u.DB.RunningUpdate(ctx); err == nil && running != nil {
		return "", fmt.Errorf("an upgrade to %s is already running", running.ToVersion)
	}

	jobName := fmt.Sprintf("vesta-upgrade-%s-%d", strings.ReplaceAll(req.Version, ".", "-"), time.Now().Unix())

	if _, err := u.DB.CreateUpdateRecord(ctx, u.FromVersion, req.Version, jobName, req.TriggeredBy); err != nil {
		return "", fmt.Errorf("recording upgrade: %w", err)
	}

	job := u.buildJob(jobName, req)
	if _, err := u.Clientset.BatchV1().Jobs(req.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		u.DB.FinishUpdateRecord(ctx, jobName, "failed", err.Error())
		return "", fmt.Errorf("creating upgrade job: %w", err)
	}

	go u.watch(jobName, req.Namespace)
	return jobName, nil
}

func (u *Updater) buildJob(jobName string, req UpdateRequest) *batchv1.Job {
	backoff := int32(0) // A failed upgrade must not be retried automatically.
	ttl := int32(86400) // Keep it a day so the logs are there to read afterwards.
	deadline := int64(updateJobTimeout.Seconds())

	// --reset-then-reuse-values, not --reuse-values: the latter carries the old values
	// forward wholesale and silently drops new chart defaults, which is how an upgrade
	// ends up running new images against last release's configuration.
	script := fmt.Sprintf(`set -eu
echo "Upgrading %s to %s..."
helm upgrade %s %s \
  --version %s \
  --namespace %s \
  --reset-then-reuse-values \
  --wait \
  --timeout 15m
echo "Upgrade complete."`,
		releaseName(), req.Version, releaseName(), chartRef, req.Version, req.Namespace)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: req.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":      "vesta",
				"app.kubernetes.io/component":    "upgrade",
				"kubernetes.getvesta.sh/upgrade": "true",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/part-of":   "vesta",
						"app.kubernetes.io/component": "upgrade",
						"job-name":                    jobName,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// A separate, narrower identity than the one the API and operator
					// share -- see upgrader-rbac.yaml.
					ServiceAccountName: releaseName() + "-upgrader",
					Containers: []corev1.Container{{
						Name:    "helm",
						Image:   helmImage(),
						Command: []string{"sh", "-c", script},
					}},
				},
			},
		},
	}
}

// watch reconciles the Job outcome into the history table.
//
// Best-effort by design: the upgrade restarts the API, so this goroutine is usually killed
// partway through. reconcileOnStartup picks up whatever it left behind.
func (u *Updater) watch(jobName, namespace string) {
	ctx := context.Background()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline := time.After(updateJobTimeout)

	for {
		select {
		case <-deadline:
			u.DB.FinishUpdateRecord(ctx, jobName, "failed", "upgrade timed out")
			return
		case <-ticker.C:
			job, err := u.Clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				continue
			}
			for _, cond := range job.Status.Conditions {
				if cond.Status != corev1.ConditionTrue {
					continue
				}
				switch cond.Type {
				case batchv1.JobComplete:
					u.DB.FinishUpdateRecord(ctx, jobName, "succeeded", "")
					return
				case batchv1.JobFailed:
					u.DB.FinishUpdateRecord(ctx, jobName, "failed", cond.Message)
					return
				}
			}
		}
	}
}

// ReconcileOnStartup closes out an upgrade whose watcher died with the old API process.
//
// Without this, the record started before the restart would sit at "running" forever and
// block every future upgrade on the already-running check.
func (u *Updater) ReconcileOnStartup(ctx context.Context, namespace string) {
	running, err := u.DB.RunningUpdate(ctx)
	if err != nil || running == nil || running.JobName == "" {
		return
	}

	job, err := u.Clientset.BatchV1().Jobs(namespace).Get(ctx, running.JobName, metav1.GetOptions{})
	if err != nil {
		// The Job is gone -- most likely its TTL expired while we were down. The upgrade
		// evidently finished; whether it succeeded is visible in the running version.
		u.DB.FinishUpdateRecord(ctx, running.JobName, "unknown", "upgrade job no longer exists")
		return
	}

	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			u.DB.FinishUpdateRecord(ctx, running.JobName, "succeeded", "")
			return
		case batchv1.JobFailed:
			u.DB.FinishUpdateRecord(ctx, running.JobName, "failed", cond.Message)
			return
		}
	}

	// Still going: pick the watch back up in this process.
	log.Printf("resuming watch on in-flight upgrade job %s", running.JobName)
	go u.watch(running.JobName, namespace)
}
