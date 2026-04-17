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
	items, err := w.DB.GetPendingScheduledDeployments()
	if err != nil {
		log.Printf("[scheduler] Error fetching pending deployments: %v", err)
		return
	}

	for _, sd := range items {
		w.execute(sd)
	}
}

func (w *ScheduledDeploymentWorker) execute(sd db.ScheduledDeployment) {
	log.Printf("[scheduler] Executing scheduled deployment %s for app %s", sd.ID, sd.AppID)

	if err := w.DB.UpdateScheduledDeploymentStatus(sd.ID, "running", ""); err != nil {
		log.Printf("[scheduler] Error updating status to running: %v", err)
		return
	}

	image := sd.Image
	tag := sd.Tag
	if tag == "" {
		tag = "latest"
	}

	// Patch the VestaApp to update the image (triggers reconciliation by operator)
	patch := fmt.Sprintf(`{"spec":{"image":{"repository":"%s","tag":"%s"}}}`, image, tag)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := w.K8s.PatchResource(ctx, k8s.VestaAppGVR, vestaSystemNS, sd.AppID, []byte(patch))
	if err != nil {
		msg := fmt.Sprintf("Failed to deploy: %v", err)
		log.Printf("[scheduler] %s", msg)
		_ = w.DB.UpdateScheduledDeploymentStatus(sd.ID, "failed", msg)
		return
	}

	deployed := fmt.Sprintf("%s:%s", image, tag)
	_ = w.DB.UpdateScheduledDeploymentStatus(sd.ID, "completed", "Deployed "+deployed)
	log.Printf("[scheduler] Completed scheduled deployment %s", sd.ID)
}
