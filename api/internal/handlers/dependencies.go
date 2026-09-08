package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

type DependencyNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // "app" or "external"
}

type DependencyEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"` // env var key that references the dependency
}

func (h *Handler) GetAppDependencies(c *gin.Context) {
	projectID := c.Param("projectId")

	// List all apps in this project
	apps, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS,
		"kubernetes.getvesta.sh/project="+projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// Build a map of app names for reference checking
	appNames := make(map[string]bool)
	for _, app := range apps.Items {
		appNames[app.GetName()] = true
	}

	nodes := []DependencyNode{}
	edges := []DependencyEdge{}
	seenExternals := make(map[string]bool)

	for _, app := range apps.Items {
		appName := app.GetName()
		nodes = append(nodes, DependencyNode{
			ID:   appName,
			Name: appName,
			Type: "app",
		})

		spec, _, _ := unstructuredNestedMap(app.Object, "spec")
		runtime, _, _ := unstructuredNestedMap(spec, "runtime")
		envVars, _, _ := unstructuredNestedMap(runtime, "envVars")

		for key, val := range envVars {
			valStr, ok := val.(string)
			if !ok {
				continue
			}

			// Check if env var value references another app's service DNS
			for otherApp := range appNames {
				if otherApp == appName {
					continue
				}
				if strings.Contains(valStr, otherApp+".") || strings.Contains(valStr, otherApp+":") || valStr == otherApp {
					edges = append(edges, DependencyEdge{
						From:  appName,
						To:    otherApp,
						Label: key,
					})
				}
			}

			// Detect external services from common patterns
			if isExternalURL(valStr) {
				ext := extractHostFromURL(valStr)
				if ext != "" && !appNames[ext] && !seenExternals[ext] {
					seenExternals[ext] = true
					nodes = append(nodes, DependencyNode{
						ID:   "ext-" + ext,
						Name: ext,
						Type: "external",
					})
				}
				if ext != "" {
					edges = append(edges, DependencyEdge{
						From:  appName,
						To:    "ext-" + ext,
						Label: key,
					})
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes": nodes,
		"edges": edges,
	})
}

func isExternalURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "postgresql://") || strings.HasPrefix(s, "postgres://") ||
		strings.HasPrefix(s, "mysql://") || strings.HasPrefix(s, "mongodb://") ||
		strings.HasPrefix(s, "redis://") || strings.HasPrefix(s, "amqp://") ||
		strings.HasPrefix(s, "kafka://")
}

func extractHostFromURL(s string) string {
	// Strip protocol
	idx := strings.Index(s, "://")
	if idx >= 0 {
		s = s[idx+3:]
	}
	// Strip auth
	if atIdx := strings.Index(s, "@"); atIdx >= 0 {
		s = s[atIdx+1:]
	}
	// Strip path
	if slashIdx := strings.Index(s, "/"); slashIdx >= 0 {
		s = s[:slashIdx]
	}
	// Strip port
	if colonIdx := strings.LastIndex(s, ":"); colonIdx >= 0 {
		s = s[:colonIdx]
	}
	return s
}
