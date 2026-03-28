package handlers

import (
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/services"
)

type Handler struct {
	K8s      *k8s.Client
	DB       *db.DB
	Notifier *services.Notifier
}

func New(kc *k8s.Client, database *db.DB, notifier *services.Notifier) *Handler {
	return &Handler{K8s: kc, DB: database, Notifier: notifier}
}
