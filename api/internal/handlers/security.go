package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// Platform security posture.
//
// Two settings, both off by default and both consequential enough that the UI has to be able
// to explain what turning them on will do:
//
//   - the pod hardening profile, which changes how every app's containers are admitted
//   - network isolation, which stops environments reaching each other
//
// GetSecurityPosture reports the second one's *observed* state as well as its configured one,
// because they are genuinely different questions: NetworkPolicy is enforced by the cluster's
// network plugin rather than by Kubernetes, and a cluster running one that ignores it accepts
// every policy and filters nothing.

var validProfiles = map[string]bool{"legacy": true, "baseline": true, "restricted": true}

// GetSecurityPosture reports the configured posture and what the operator observed.
func (h *Handler) GetSecurityPosture(c *gin.Context) {
	ctx := c.Request.Context()

	out := gin.H{
		"profile":            "legacy",
		"defaultSecretScope": SecretScopeGlobal,
		"networkIsolation":   gin.H{"enabled": false},
	}

	if cfg, err := h.K8s.GetClusterResource(ctx, k8s.VestaConfigGVR, vestaConfigName); err == nil {
		if profile, _, _ := unstructured.NestedString(cfg.Object, "spec", "security", "profile"); profile != "" {
			out["profile"] = profile
		}
		if scope, _, _ := unstructured.NestedString(cfg.Object, "spec", "security", "defaultSecretScope"); scope != "" {
			out["defaultSecretScope"] = ResolveSecretScope("", scope)
		}
		if ni, found, _ := unstructured.NestedMap(cfg.Object, "spec", "security", "networkIsolation"); found {
			out["networkIsolation"] = ni
		}
	}

	// What isolation is actually doing, gathered from the environments the operator has
	// reconciled. Configured-but-not-enforced is the state worth surfacing loudest: the
	// policies exist, they are visible in kubectl, and nothing is being filtered.
	envs, err := h.K8s.ListResources(ctx, k8s.VestaEnvironmentGVR, vestaSystemNS, "")
	if err == nil {
		var observed []gin.H
		for i := range envs.Items {
			status, found, _ := unstructured.NestedMap(envs.Items[i].Object, "status", "networkIsolation")
			if !found {
				continue
			}
			status["environment"] = envs.Items[i].GetName()
			observed = append(observed, gin.H(status))
		}
		out["observed"] = observed
	}

	c.JSON(http.StatusOK, out)
}

// SetSecurityPosture writes the platform-wide posture.
func (h *Handler) SetSecurityPosture(c *gin.Context) {
	var req struct {
		Profile            *string `json:"profile"`
		DefaultSecretScope *string `json:"defaultSecretScope"`
		NetworkIsolation   *struct {
			Enabled           bool     `json:"enabled"`
			TrustedNamespaces []string `json:"trustedNamespaces"`
			MetricsPort       int32    `json:"metricsPort"`
		} `json:"networkIsolation"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	security := map[string]interface{}{}

	if req.Profile != nil {
		if !validProfiles[*req.Profile] {
			// Rejected here rather than left to the CRD enum, so the message says what the
			// choices are instead of surfacing an admission webhook error.
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code:    400,
				Message: "profile must be one of legacy, baseline or restricted",
			})
			return
		}
		security["profile"] = *req.Profile
	}

	if req.DefaultSecretScope != nil {
		scope := *req.DefaultSecretScope
		if scope != SecretScopeGlobal && scope != SecretScopeProject {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code: 400, Message: "defaultSecretScope must be global or project"})
			return
		}
		security["defaultSecretScope"] = scope
	}

	if req.NetworkIsolation != nil {
		ni := map[string]interface{}{"enabled": req.NetworkIsolation.Enabled}
		if len(req.NetworkIsolation.TrustedNamespaces) > 0 {
			ni["trustedNamespaces"] = req.NetworkIsolation.TrustedNamespaces
		}
		if req.NetworkIsolation.MetricsPort > 0 {
			ni["metricsPort"] = req.NetworkIsolation.MetricsPort
		}
		security["networkIsolation"] = ni
	}

	if len(security) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "nothing to set"})
		return
	}

	ctx := c.Request.Context()

	// Same reasoning as the default issuer: the operator's resolver reads an arbitrary
	// VestaConfig, so writing to one of several would take effect unpredictably.
	list, err := h.K8s.ListResources(ctx, k8s.VestaConfigGVR, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if len(list.Items) > 1 {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: "more than one VestaConfig exists; the operator reads an arbitrary one, so the posture cannot be set safely",
		})
		return
	}
	if len(list.Items) == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no VestaConfig found; install the Helm chart first"})
		return
	}

	patch, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{"security": security},
	})
	if _, err := h.K8s.PatchClusterResource(ctx, k8s.VestaConfigGVR, list.Items[0].GetName(), patch); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "set_security_posture", "platform", "security", "", "", "", security)

	c.JSON(http.StatusOK, gin.H{
		"status": "saved",
		// Apps are hardened as they reconcile, not immediately, and a profile change only
		// reaches a running pod when its Deployment is next updated.
		"note": "new settings apply to each app as it is next reconciled",
	})
}
