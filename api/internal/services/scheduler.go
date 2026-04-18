package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
)

const vestaSystemNS = "vesta-system"

type ScheduledDeploymentWorker struct {
	DB  *db.DB
	K8s *k8s.Client
}

func (w *ScheduledDeploymentWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Println("[scheduler] Scheduled deployment worker started")
	for {
		select {
		case <-ctx.Done():
			log.Println("[scheduler] Worker stopped")
			return
		case <-ticker.C:
			w.processPending()
		}
	}
}

func (w *ScheduledDeploymentWorker) processPending() {
	items, err := w.DB.GetPendingScheduledDeployments(context.Background())
	if err != nil {
		log.Printf("[scheduler] Error fetching pending deployments: %v", err)
		return
	}

	for _, sd := range items {
		w.execute(sd)
	}
}

func (w *ScheduledDeploymentWorker) execute(sd db.ScheduledDeployment) {
	log.Printf("[scheduler] Executing scheduled deployment %s for app %s env %s", sd.ID, sd.AppID, sd.Environment)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := w.DB.UpdateScheduledDeploymentStatus(ctx, sd.ID, "running", ""); err != nil {
		log.Printf("[scheduler] Error updating status to running: %v", err)
		return
	}

	tag := sd.Tag
	if tag == "" {
		tag = "latest"
	}

	// Get the current VestaApp to patch per-environment image
	app, err := w.K8s.GetResource(ctx, k8s.VestaAppGVR, vestaSystemNS, sd.AppID)
	if err != nil {
		msg := fmt.Sprintf("Failed to get app: %v", err)
		log.Printf("[scheduler] %s", msg)
		_ = w.DB.UpdateScheduledDeploymentStatus(ctx, sd.ID, "failed", msg)
		return
	}

	spec, ok := app.Object["spec"].(map[string]interface{})
	if !ok {
		spec = map[string]interface{}{}
	}

	// Determine image repository
	repo := sd.Image
	if repo == "" {
		if imageSpec, ok := spec["image"].(map[string]interface{}); ok {
			repo, _ = imageSpec["repository"].(string)
		}
	}

	// Patch per-environment image tag (matching DeployApp pattern)
	if sd.Environment != "" {
		envs, _ := spec["environments"].([]interface{})
		updated := false
		for i, raw := range envs {
			envMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := envMap["name"].(string)
			if name == sd.Environment {
				envImage, _ := envMap["image"].(map[string]interface{})
				if envImage == nil {
					envImage = map[string]interface{}{}
				}
				if repo != "" {
					envImage["repository"] = repo
				}
				envImage["tag"] = tag
				envMap["image"] = envImage
				envs[i] = envMap
				updated = true
				break
			}
		}
		if !updated {
			// Env entry not found — fall back to patching global image
			imageSpec, _ := spec["image"].(map[string]interface{})
			if imageSpec == nil {
				imageSpec = map[string]interface{}{}
			}
			if repo != "" {
				imageSpec["repository"] = repo
			}
			imageSpec["tag"] = tag
			spec["image"] = imageSpec
		} else {
			spec["environments"] = envs
		}
	} else {
		// No environment specified — patch global image
		imageSpec, _ := spec["image"].(map[string]interface{})
		if imageSpec == nil {
			imageSpec = map[string]interface{}{}
		}
		if repo != "" {
			imageSpec["repository"] = repo
		}
		imageSpec["tag"] = tag
		spec["image"] = imageSpec
	}

	// Also update global image tag so status reflects latest
	if repo != "" {
		imageSpec, _ := spec["image"].(map[string]interface{})
		if imageSpec == nil {
			imageSpec = map[string]interface{}{}
		}
		imageSpec["tag"] = tag
		spec["image"] = imageSpec
	}

	app.Object["spec"] = spec
	_, err = w.K8s.UpdateResource(ctx, k8s.VestaAppGVR, vestaSystemNS, app)
	if err != nil {
		msg := fmt.Sprintf("Failed to deploy: %v", err)
		log.Printf("[scheduler] %s", msg)
		_ = w.DB.UpdateScheduledDeploymentStatus(ctx, sd.ID, "failed", msg)
		return
	}

	deployed := fmt.Sprintf("%s:%s", repo, tag)
	_ = w.DB.UpdateScheduledDeploymentStatus(ctx, sd.ID, "completed", "Deployed "+deployed)
	log.Printf("[scheduler] Completed scheduled deployment %s", sd.ID)
}
