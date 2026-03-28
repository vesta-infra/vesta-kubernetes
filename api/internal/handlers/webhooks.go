package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) ReceiveWebhook(c *gin.Context) {
	provider := c.Param("provider")

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "cannot read body"})
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid JSON"})
		return
	}

	switch provider {
	case "github":
		h.handleGitHubWebhook(c, body, payload)
	case "gitlab":
		h.handleGitLabWebhook(c, body, payload)
	default:
		c.JSON(http.StatusOK, gin.H{"provider": provider, "status": "received"})
	}
}

func (h *Handler) handleGitHubWebhook(c *gin.Context, body []byte, payload map[string]interface{}) {
	event := c.GetHeader("X-GitHub-Event")

	if secret := c.GetHeader("X-Hub-Secret"); secret != "" {
		sig := c.GetHeader("X-Hub-Signature-256")
		if !verifyGitHubSignature(body, sig, secret) {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: "invalid signature"})
			return
		}
	}

	switch event {
	case "push":
		ref, _ := payload["ref"].(string)
		repo, _ := payload["repository"].(map[string]interface{})
		fullName, _ := repo["full_name"].(string)

		headCommit, _ := payload["head_commit"].(map[string]interface{})
		commitSHA, _ := headCommit["id"].(string)

		envs, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
			return
		}

		matched := false
		for _, e := range envs.Items {
			eSpec, _, _ := unstructuredNestedMap(e.Object, "spec")
			branch, _ := eSpec["branch"].(string)
			autoDeploy, _ := eSpec["autoDeploy"].(bool)
			project, _ := eSpec["project"].(string)
			envLabel := e.GetLabels()["kubernetes.getvesta.sh/environment"]

			if !autoDeploy || fmt.Sprintf("refs/heads/%s", branch) != ref {
				continue
			}

			namespace := fmt.Sprintf("%s-%s", project, envLabel)
			apps, _ := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS,
				"kubernetes.getvesta.sh/project="+project)
			if apps != nil {
				for _, app := range apps.Items {
					appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
					gitSpec, _, _ := unstructuredNestedMap(appSpec, "git")
					if gitSpec == nil {
						continue
					}
					appRepo, _ := gitSpec["repository"].(string)
					if appRepo != fullName {
						continue
					}

					_ = namespace
					gitSpec["commitSHA"] = commitSHA
					appSpec["git"] = gitSpec
					app.Object["spec"] = appSpec
					h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, &app)
					matched = true
				}
			}
		}

		status := "no matching project"
		if matched {
			status = "deploy triggered"
		}
		c.JSON(http.StatusOK, gin.H{"status": status, "event": event})

	case "pull_request":
		c.JSON(http.StatusOK, gin.H{"status": "received", "event": event, "message": "PR app handling not yet implemented"})

	default:
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "event": event})
	}
}

func (h *Handler) handleGitLabWebhook(c *gin.Context, body []byte, payload map[string]interface{}) {
	c.JSON(http.StatusOK, gin.H{"status": "received", "provider": "gitlab"})
}

func verifyGitHubSignature(body []byte, signature string, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
