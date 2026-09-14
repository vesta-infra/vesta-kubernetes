package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/git"
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

	// Which connection signed this?
	//
	// A single global App used to make the question meaningless. With several connections
	// the secret to verify against depends on which one the hook belongs to, and trying
	// each in turn is not an option: GitLab's token check is a plain equality test, so
	// "try them all" turns the endpoint into an oracle that confirms a guessed secret.
	//
	// New connections therefore get their own URL. The connection-less path is what hooks
	// created before this release use, and it resolves to the adopted row.
	conn, connErr := h.resolveWebhookConnection(c.Request.Context(), provider, c.Param("connectionId"))

	// Record webhook delivery
	delivery := db.WebhookDelivery{
		Provider:  provider,
		Payload:   payload,
		Status:    "received",
		IPAddress: c.ClientIP(),
	}

	// What the admin webhook log shows about this delivery.
	//
	// This used to be a fourth hand-written payload parser, and a partial one: GitLab
	// filled in only an event type so its deliveries showed a blank repository and branch,
	// Bitbucket had no case at all, and the branch was taken as ref[11:] behind a length
	// check -- so refs/tags/v1 was logged as branch "v1".
	//
	// The provider already reduces a payload to exactly these fields, so it does it here
	// too. Best effort: this runs before verification on purpose, so an unauthenticated
	// delivery is still recorded, and a payload that will not parse is still worth a row.
	delivery.EventType = webhookEventHeader(c, provider)
	delivery.DeliveryID = c.GetHeader("X-GitHub-Delivery")
	if impl, ok := h.GitProviders.Get(provider); ok && connErr == nil {
		if ev, err := impl.ParsePush(c.Request.Header, body, conn); err == nil {
			delivery.Repository = ev.Repo.Path
			delivery.Branch = ev.Branch
			delivery.CommitSHA = ev.CommitSHA
		}
	}

	deliveryID, _ := h.DB.InsertWebhookDelivery(c.Request.Context(), delivery)

	if connErr != nil {
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "failed", connErr.Error(), nil, durationMs)
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: connErr.Error()})
		return
	}

	impl, ok := h.GitProviders.Get(provider)
	if !ok {
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "ignored", "unknown provider", nil, durationMs)
		c.JSON(http.StatusOK, gin.H{"provider": provider, "status": "received"})
		return
	}

	// Authenticate, then parse, then match. One path for every provider: the parts that
	// genuinely differ -- the signature scheme and the payload shape -- are the provider's,
	// and everything after is the same work regardless of who sent it.
	if !h.authenticateDelivery(c, impl, conn, body, deliveryID, startTime) {
		return
	}

	ev, err := impl.ParsePush(c.Request.Header, body, conn)
	if err != nil {
		durationMs := int(time.Since(startTime).Milliseconds())
		if errors.Is(err, git.ErrNotAPush) {
			// A pull request, a tag, a ping. Valid, just not ours.
			h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "ignored", "not a push event", nil, durationMs)
			c.JSON(http.StatusOK, gin.H{"provider": provider, "status": "ignored"})
			return
		}
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "failed", err.Error(), nil, durationMs)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	h.dispatchPush(c, ev, conn, deliveryID, startTime)
}

// authenticateDelivery applies the instance's policy on top of the provider's verification.
//
// The split matters. Whether a signature is valid is the provider's question; whether a
// delivery with no signature at all may proceed is the instance's, because only the handler
// knows this install is mid-upgrade and still tolerating them.
func (h *Handler) authenticateDelivery(c *gin.Context, impl git.Provider, conn git.Connection,
	body []byte, deliveryID string, startTime time.Time) bool {

	reject := func(reason string) bool {
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "failed", reason, nil, durationMs)
		c.JSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: reason})
		return false
	}

	secret := h.webhookSecretFor(c.Request.Context(), conn)
	err := impl.VerifyWebhook(c.Request.Header, body, secret)

	connectionScoped := c.Param("connectionId") != "" && conn.ID != ""
	allowUnsigned := h.DB.GetBoolSetting(c.Request.Context(), db.SettingWebhooksAllowUnsigned, true)

	switch verdict, reason := webhookPolicy(err, connectionScoped, allowUnsigned); verdict {
	case webhookReject:
		return reject(reason)
	case webhookAcceptUnsigned:
		// Recorded rather than merely allowed, so Settings can show how much of this is
		// still happening before an admin closes the door.
		durationMs := int(time.Since(startTime).Milliseconds())
		h.DB.UpdateWebhookDelivery(c.Request.Context(), deliveryID, "unsigned", reason, nil, durationMs)
	}
	return true
}

// dispatchPush matches a push against every environment that auto-deploys from its branch,
// and triggers a build or a deploy for each app on that repository.
//
// Provider-agnostic: by the time it runs the delivery is authenticated and the payload has
// been reduced to a PushEvent, so this is the same work whoever sent it.
func (h *Handler) dispatchPush(c *gin.Context, ev *git.PushEvent, conn git.Connection, deliveryID string, startTime time.Time) {
	// Already normalised by the provider, so every comparison below is against one form.
	pushRef := ev.Repo
	commitSHA := ev.CommitSHA
	ref := ev.Ref

	{
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
					// Match on normalised identity, not on the two raw strings.
					//
					// The old comparison was `appRepo != fullName`, which meant a
					// repository entered as a URL, with a trailing .git, or in a different
					// case never matched the push that should have deployed it -- and said
					// nothing when it did not. It also had no provider dimension, so once
					// more than one provider exists, acme/web on GitLab would match a
					// GitHub-backed app.
					appRepo, _ := gitSpec["repository"].(string)
					appProvider, _ := gitSpec["provider"].(string)
					if appProvider == "" {
						appProvider = git.ProviderGitHub
					}
					appHost, _ := gitSpec["host"].(string)

					appTokenSecret, _ := gitSpec["tokenSecret"].(string)
					appConnectionID, _ := gitSpec["connectionId"].(string)

					appRef, err := git.ParseRepoRef(appProvider, appHost, appRepo)
					if err != nil {
						// A repository nobody can parse can never match anything, which is
						// worth one line in the log rather than silence -- this is where a
						// half-filled create form ends up.
						log.Printf("[webhook] app %s has an unusable repository %q: %v",
							app.GetName(), appRepo, err)
						continue
					}
					if !appRef.Equal(pushRef) {
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
							AppID:       appName,
							ProjectID:   project,
							Environment: envLabel,
							Strategy:    strategy,
							// The normalised ref, not the raw payload string: the clone
							// URL is built from these, and a host is what makes a
							// self-managed server reachable at all.
							Repository:     appRef.Path,
							Provider:       appRef.Provider,
							GitSecretName:  appTokenSecret,
							Host:           appRef.Host,
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

							// Tell the host a build has started. Through the provider, so
							// the state vocabulary and the auth scheme are its problem
							// rather than being assumed here.
							if p, ok := h.GitProviders.Get(appRef.Provider); ok {
								if err := p.ReportStatus(c.Request.Context(), h.connectionFor(c.Request.Context(), appRef, appConnectionID),
									appRef, commitSHA, git.StateRunning, "",
									fmt.Sprintf("Vesta build started for %s", envLabel)); err != nil {
									log.Printf("[webhook] could not report build status for %s: %v", appName, err)
								}
							}
						}
					} else {
						// Pre-built image: CI built and pushed it, so the push event only
						// has to point the environment at the new tag.
						//
						// This used to write spec.git.commitSHA, a field the CRD does not
						// declare -- the API server pruned it, nothing else read it, and
						// nothing deployed. The endpoint still answered "deploy triggered",
						// so the failure was invisible from both ends.
						//
						// The tag is the short SHA because that is the convention Vesta
						// already sets when it builds an image itself (see the imageDest
						// above). An image tagged some other way will not be found, and
						// surfaces as ImagePullBackOff with the usual diagnostics rather
						// than as silence.
						shortSHA := commitSHA
						if len(shortSHA) > 8 {
							shortSHA = shortSHA[:8]
						}
						if shortSHA == "" {
							log.Printf("[webhook] no commit SHA for %s; nothing to deploy", appName)
						} else if err := h.Builder.PinEnvironmentTag(c.Request.Context(), appName, envLabel, shortSHA); err != nil {
							log.Printf("[webhook] deploy failed for %s/%s: %v", appName, envLabel, err)
						} else if err := h.Builder.RecordCommitSHA(c.Request.Context(), appName, commitSHA); err != nil {
							log.Printf("[webhook] failed to record commit SHA for %s: %v", appName, err)
						}
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
			"event":           "push",
			"branch":          ev.Branch,
			"appsTriggered":   appsTriggered,
			"buildsTriggered": buildsTriggered,
		})

	}
}

// webhookVerdict is what the instance's policy decided about an inbound delivery.
type webhookVerdict int

const (
	webhookAccept webhookVerdict = iota
	webhookAcceptUnsigned
	webhookReject
)

// webhookPolicy decides what to do with a provider's verification result.
//
// Separated from the provider because the two answer different questions. Whether a
// signature is valid is the provider's; whether a delivery carrying none may proceed is the
// instance's, and only the handler knows this install is mid-upgrade.
//
// The rules, in order:
//
//   - An invalid signature is always refused. No flag relaxes that.
//   - A missing one is accepted on a connection-scoped URL, because the unguessable
//     connection id is then the credential. This is what makes Bitbucket Cloud usable at
//     all: it offers no webhook secret, so there is nothing else available.
//   - Otherwise a missing signature is refused, unless the instance still tolerates
//     unsigned deliveries -- which only upgrades do, and only until an admin turns it off.
//
// The historical hole was the opposite of all this: an absent signature header skipped
// verification entirely, so an unauthenticated POST triggered builds and deploys on any app
// whose repository string the caller could guess.
func webhookPolicy(verifyErr error, connectionScoped, allowUnsigned bool) (webhookVerdict, string) {
	if verifyErr == nil {
		return webhookAccept, ""
	}
	if !errors.Is(verifyErr, git.ErrNoSignature) {
		return webhookReject, verifyErr.Error()
	}
	if connectionScoped {
		return webhookAccept, ""
	}
	if !allowUnsigned {
		return webhookReject, "missing signature"
	}
	return webhookAcceptUnsigned, "accepted without a signature"
}

// webhookEventHeader returns the event name, which every provider puts in a header of its
// own naming.
func webhookEventHeader(c *gin.Context, provider string) string {
	switch provider {
	case git.ProviderGitHub:
		return c.GetHeader("X-GitHub-Event")
	case git.ProviderGitLab:
		return c.GetHeader("X-Gitlab-Event")
	case git.ProviderBitbucket:
		return c.GetHeader("X-Event-Key")
	}
	return ""
}
