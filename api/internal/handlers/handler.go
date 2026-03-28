package handlers

import (
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
)

type Handler struct {
	K8s *k8s.Client
	DB  *db.DB
}

func New(kc *k8s.Client, database *db.DB) *Handler {
	return &Handler{K8s: kc, DB: database}
}
