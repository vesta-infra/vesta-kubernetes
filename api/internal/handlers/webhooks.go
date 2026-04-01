package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

func (h *Handler) ReceiveWebhook(c *gin.Context) {
	provider := c.Param("provider")
	startTime := time.Now()

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

	// Record webhook delivery
	delivery := db.WebhookDelivery{
		Provider:  provider,
		Payload:   payload,
		Status:    "received",
		IPAddress: c.ClientIP(),
	}

	switch provider {
	case "github":
		delivery.EventType = c.GetHeader("X-GitHub-Event")
		delivery.DeliveryID = c.GetHeader("X-GitHub-Delivery")
		if repo, ok := payload["repository"].(map[string]interface{}); ok {
			delivery.Repository, _ = repo["full_name"].(string)
		}
		if ref, ok := payload["ref"].(string); ok && len(ref) > 11 {
			delivery.Branch = ref[11:] // strip refs/heads/
		}
		if hc, ok := payload["head_commit"].(map[string]interface{}); ok {
			delivery.CommitSHA, _ = hc["id"].(string)
		}
	case "gitlab":
		delivery.EventType = c.GetHeader("X-Gitlab-Event")
	}

	deliveryID, _ := h.DB.InsertWebhookDelivery(c.Request.Context(), delivery)

	switch provider {
	case "github":
		h.handleGitHubWebhook(c, body, payload, deliveryID, startTime)
	case "gitlab":
		h.handleGitLabWebhook(c, body, payload, deliveryID, startTime)
	default:
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "ignored", "unknown provider", nil, durationMs)
		c.JSON(http.StatusOK, gin.H{"provider": provider, "status": "received"})
	}
}

func (h *Handler) handleGitHubWebhook(c *gin.Context, body []byte, payload map[string]interface{}, deliveryID string, startTime time.Time) {
	event := c.GetHeader("X-GitHub-Event")

	if secret := c.GetHeader("X-Hub-Secret"); secret != "" {
		sig := c.GetHeader("X-Hub-Signature-256")
		if !verifyGitHubSignature(body, sig, secret) {
			durationMs := int(time.Since(startTime).Milliseconds())
			h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "failed", "invalid signature", nil, durationMs)
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
			durationMs := int(time.Since(startTime).Milliseconds())
			h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "failed", err.Error(), nil, durationMs)
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
			return
		}

		appsTriggered := []string{}
		buildsTriggered := []string{}
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
					appName := app.GetName()

					// Check if app has a build strategy configured
					buildSpec, _, _ := unstructuredNestedMap(appSpec, "build")
					strategy := ""
					if buildSpec != nil {
						strategy, _ = buildSpec["strategy"].(string)
					}

					if strategy != "" && strategy != "image" {
						// Trigger a build — the builder will auto-deploy on success
						imageSpec, _, _ := unstructuredNestedMap(appSpec, "image")
						imageRepo := ""
						registrySecret := ""
						dockerfile := "Dockerfile"

						if imageSpec != nil {
							imageRepo, _ = imageSpec["repository"].(string)
							if ps, ok := imageSpec["imagePullSecrets"].([]interface{}); ok && len(ps) > 0 {
								if first, ok := ps[0].(map[string]interface{}); ok {
									registrySecret, _ = first["name"].(string)
								}
							}
						}
						if buildSpec != nil {
							if d, ok := buildSpec["dockerfile"].(string); ok && d != "" {
								dockerfile = d
							}
						}

						shortSHA := commitSHA
						if len(shortSHA) > 8 {
							shortSHA = shortSHA[:8]
						}
						imageDest := fmt.Sprintf("%s:%s", imageRepo, shortSHA)

						buildReq := services.BuildRequest{
							AppID:          appName,
							ProjectID:      project,
							Environment:    envLabel,
							Strategy:       strategy,
							Repository:     fullName,
							Branch:         branch,
							CommitSHA:      commitSHA,
							Dockerfile:     dockerfile,
							ImageDest:      imageDest,
							RegistrySecret: registrySecret,
							TriggeredBy:    "webhook:github",
						}

						buildID, err := h.Builder.TriggerBuild(c.Request.Context(), buildReq)
						if err != nil {
							log.Printf("[webhook] build trigger failed for %s: %v", appName, err)
						} else {
							buildsTriggered = append(buildsTriggered, buildID)

							// Notify GitHub with pending status
							if gitToken := h.getGitToken(c.Request.Context(), appSpec); gitToken != "" {
								h.GitHubNotifier.NotifyBuildStatus(c.Request.Context(),
									gitToken, fullName, commitSHA, envLabel,
									"pending", "", "",
									fmt.Sprintf("Vesta build started for %s", envLabel))
							}
						}
					} else {
						// No build strategy — just update commitSHA (pre-built image flow)
						gitSpec["commitSHA"] = commitSHA
						appSpec["git"] = gitSpec
						app.Object["spec"] = appSpec
						h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, &app)
					}

					appsTriggered = append(appsTriggered, appName)
				}
			}
		}

		status := "no matching project"
		if len(buildsTriggered) > 0 {
			status = fmt.Sprintf("builds triggered: %d", len(buildsTriggered))
		} else if len(appsTriggered) > 0 {
			status = "deploy triggered"
		}

		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "processed", status, appsTriggered, durationMs)

		c.JSON(http.StatusOK, gin.H{
			"status":          status,
			"event":           event,
			"appsTriggered":   appsTriggered,
			"buildsTriggered": buildsTriggered,
		})

	case "pull_request":
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "ignored", "PR handling not implemented", nil, durationMs)
		c.JSON(http.StatusOK, gin.H{"status": "received", "event": event, "message": "PR app handling not yet implemented"})

	default:
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "ignored", "unhandled event type", nil, durationMs)
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "event": event})
	}
}

func (h *Handler) handleGitLabWebhook(c *gin.Context, body []byte, payload map[string]interface{}, deliveryID string, startTime time.Time) {
	durationMs := int(time.Since(startTime).Milliseconds())
	h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "ignored", "gitlab not implemented", nil, durationMs)
	c.JSON(http.StatusOK, gin.H{"status": "received", "provider": "gitlab"})
}

func verifyGitHubSignature(body []byte, signature string, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// getGitToken tries to extract a GitHub token from the app's git secret reference.
func (h *Handler) getGitToken(ctx context.Context, appSpec map[string]interface{}) string {
	gitSpec, _, _ := unstructuredNestedMap(appSpec, "git")
	if gitSpec == nil {
		return ""
	}
	secretName, _ := gitSpec["tokenSecret"].(string)
	if secretName == "" {
		return ""
	}
	secret, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if token, ok := secret.Data["token"]; ok {
		return string(token)
	}
	return ""
}
