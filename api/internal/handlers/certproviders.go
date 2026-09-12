package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// vestaConfigName is the singleton cluster-scoped VestaConfig the Helm chart creates.
const vestaConfigName = "vesta"

// GetCertManagerStatus reports whether cert-manager is installed, so the settings page can
// show install instructions instead of a form whose submissions would go nowhere.
func (h *Handler) GetCertManagerStatus(c *gin.Context) {
	ctx := c.Request.Context()

	_, err := h.K8s.GetClusterResource(ctx, k8s.CRDGVR, services.ClusterIssuerCRD)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusOK, gin.H{
				"installed": false,
				"namespace": h.CertProvider.Namespace(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "cannot check for cert-manager: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"installed": true,
		"namespace": h.CertProvider.Namespace(),
	})
}

// ListSSLProviders returns every ClusterIssuer in the cluster, flagging the ones Vesta
// created. Issuers made out-of-band stay selectable — hiding them would make an existing
// working setup look broken — but the UI does not offer to edit them.
func (h *Handler) ListSSLProviders(c *gin.Context) {
	ctx := c.Request.Context()

	defaultIssuer := h.instanceDefaultIssuer(ctx)

	list, err := h.K8s.ListResources(ctx, k8s.ClusterIssuerGVR, "", "")
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusOK, gin.H{"providers": []services.Provider{}, "certManagerInstalled": false})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	providers := make([]services.Provider, 0, len(list.Items))
	for i := range list.Items {
		providers = append(providers, services.ProviderFromIssuer(&list.Items[i], defaultIssuer))
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })

	c.JSON(http.StatusOK, gin.H{
		"providers":            providers,
		"certManagerInstalled": true,
		"default":              defaultIssuer,
	})
}

// CreateSSLProvider creates a ClusterIssuer and the Secret holding its credentials.
func (h *Handler) CreateSSLProvider(c *gin.Context) {
	var spec services.ProviderSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body"})
		return
	}
	if err := services.ValidateSpec(spec); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	ctx := c.Request.Context()

	// IsNotFound covers both "no such issuer" (fine, we are creating it) and "no such
	// resource type" (cert-manager absent). The create below distinguishes them: it
	// succeeds in the first case and fails in the second.
	if _, err := h.K8s.GetClusterResource(ctx, k8s.ClusterIssuerGVR, spec.Name); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: fmt.Sprintf("a certificate issuer named %q already exists", spec.Name)})
		return
	} else if !apierrors.IsNotFound(err) {
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{Code: 503, Message: "cert-manager is not available: " + err.Error()})
		return
	}

	// Credentials first: an issuer that references a Secret which does not exist yet sits
	// Pending with a message about the missing Secret, which reads like a Vesta bug.
	if err := h.CertProvider.SaveCredentials(ctx, spec); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "cannot store provider credentials: " + err.Error()})
		return
	}

	obj := h.CertProvider.BuildClusterIssuer(spec)
	if _, err := h.K8s.CreateClusterResource(ctx, k8s.ClusterIssuerGVR, obj); err != nil {
		// Roll the credentials back so a failed create does not leave an orphan Secret
		// that a later provider of the same name would silently inherit.
		_ = h.CertProvider.DeleteCredentials(ctx, spec.Name)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"name": spec.Name, "kind": spec.Kind})
	h.auditLog(c, "ssl_provider_create", "ssl_provider", spec.Name, spec.Name, "", "", auditMeta(spec))
}

// UpdateSSLProvider replaces a Vesta-managed issuer's spec.
func (h *Handler) UpdateSSLProvider(c *gin.Context) {
	name := c.Param("name")

	var spec services.ProviderSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body"})
		return
	}
	spec.Name = name
	if err := services.ValidateSpec(spec); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	ctx := c.Request.Context()

	existing, err := h.K8s.GetClusterResource(ctx, k8s.ClusterIssuerGVR, name)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "certificate issuer not found"})
		return
	}
	// Refuse to rewrite an issuer Vesta did not create. Its shape is the admin's, and
	// replacing it wholesale from a form that models only what Vesta supports would drop
	// solvers and selectors we never read.
	if !services.ProviderFromIssuer(existing, "").Managed {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: "this issuer was created outside Vesta; edit it with kubectl, or delete it and recreate it here",
		})
		return
	}

	if err := h.CertProvider.SaveCredentials(ctx, spec); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "cannot store provider credentials: " + err.Error()})
		return
	}

	// Replace the spec and refresh the labels, but keep the rest of the object — notably
	// resourceVersion, which the update needs to detect a concurrent write.
	obj := h.CertProvider.BuildClusterIssuer(spec)
	existing.Object["spec"] = obj["spec"]
	if newMeta, ok := obj["metadata"].(map[string]interface{}); ok {
		if labels, ok := newMeta["labels"].(map[string]interface{}); ok {
			stringLabels := make(map[string]string, len(labels))
			for k, v := range labels {
				if sv, ok := v.(string); ok {
					stringLabels[k] = sv
				}
			}
			existing.SetLabels(stringLabels)
		}
	}

	if _, err := h.K8s.UpdateClusterResource(ctx, k8s.ClusterIssuerGVR, existing); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "kind": spec.Kind})
	h.auditLog(c, "ssl_provider_update", "ssl_provider", name, name, "", "", auditMeta(spec))
}

// DeleteSSLProvider removes an issuer and its credentials. It refuses while apps still
// reference it, because deleting the issuer does not revoke the certificates already
// issued — the breakage surfaces weeks later as a silent renewal failure.
func (h *Handler) DeleteSSLProvider(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	if c.Query("force") != "true" {
		refs, err := h.appsReferencingIssuer(ctx, name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
			return
		}
		if len(refs) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"code": 409,
				"message": fmt.Sprintf("%d app(s) still use this provider: %s. Their certificates will stop renewing.",
					len(refs), strings.Join(refs, ", ")),
				"apps": refs,
			})
			return
		}
	}

	if h.instanceDefaultIssuer(ctx) == name {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: "this provider is the instance default; set a different default before deleting it",
		})
		return
	}

	if err := h.K8s.DeleteClusterResource(ctx, k8s.ClusterIssuerGVR, name); err != nil && !apierrors.IsNotFound(err) {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if err := h.CertProvider.DeleteCredentials(ctx, name); err != nil {
		// The issuer is already gone; report success but say the cleanup was partial.
		c.JSON(http.StatusOK, gin.H{"status": "removed", "warning": "credentials could not be fully removed: " + err.Error()})
		h.auditLog(c, "ssl_provider_delete", "ssl_provider", name, name, "", "", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "removed"})
	h.auditLog(c, "ssl_provider_delete", "ssl_provider", name, name, "", "", nil)
}

// SetDefaultSSLProvider writes VestaConfig.spec.clusterIssuer, the operator's only source
// for the instance-wide fallback.
func (h *Handler) SetDefaultSSLProvider(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body"})
		return
	}

	ctx := c.Request.Context()

	// An empty name clears the default, leaving apps with no issuer of their own without
	// TLS rather than silently pointing them at an arbitrary issuer.
	if req.Name != "" {
		if _, err := h.K8s.GetClusterResource(ctx, k8s.ClusterIssuerGVR, req.Name); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: fmt.Sprintf("no certificate issuer named %q", req.Name)})
			return
		}
	}

	// The resolver reads configList.Items[0] with no ordering, so a second VestaConfig
	// makes "the default" ambiguous. Refuse rather than write to one of them at random.
	list, err := h.K8s.ListResources(ctx, k8s.VestaConfigGVR, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if len(list.Items) > 1 {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: "more than one VestaConfig exists; the operator reads an arbitrary one, so the default cannot be set safely",
		})
		return
	}
	if len(list.Items) == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no VestaConfig found; install the Helm chart first"})
		return
	}

	patch, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{"clusterIssuer": req.Name},
	})
	if _, err := h.K8s.PatchClusterResource(ctx, k8s.VestaConfigGVR, list.Items[0].GetName(), patch); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"default": req.Name})
	h.auditLog(c, "ssl_provider_set_default", "ssl_provider", req.Name, req.Name, "", "",
		map[string]interface{}{"clusterIssuer": req.Name})
}

// instanceDefaultIssuer reads VestaConfig.spec.clusterIssuer. A missing config is not an
// error — it just means no default is set.
func (h *Handler) instanceDefaultIssuer(ctx context.Context) string {
	cfg, err := h.K8s.GetClusterResource(ctx, k8s.VestaConfigGVR, vestaConfigName)
	if err != nil {
		return ""
	}
	issuer, _, _ := unstructured.NestedString(cfg.Object, "spec", "clusterIssuer")
	return issuer
}

// appsReferencingIssuer finds every app that names this issuer, at app level or in any
// environment override, so deleting one can say what it would break.
func (h *Handler) appsReferencingIssuer(ctx context.Context, name string) ([]string, error) {
	list, err := h.K8s.ListResources(ctx, k8s.VestaAppGVR, vestaSystemNS, "")
	if err != nil {
		return nil, err
	}

	var refs []string
	for i := range list.Items {
		app := list.Items[i].Object

		if issuer, _, _ := unstructured.NestedString(app, "spec", "ingress", "clusterIssuer"); issuer == name {
			refs = append(refs, list.Items[i].GetName())
			continue
		}

		envs, _, _ := unstructured.NestedSlice(app, "spec", "environments")
		for _, e := range envs {
			env, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			if issuer, _, _ := unstructured.NestedString(env, "ingress", "clusterIssuer"); issuer == name {
				refs = append(refs, list.Items[i].GetName())
				break
			}
		}
	}

	sort.Strings(refs)
	return refs, nil
}

// auditMeta records what a provider was set to, deliberately excluding every credential
// field — the audit log is read by more people than can read Secrets.
func auditMeta(spec services.ProviderSpec) map[string]interface{} {
	solver := "http01"
	if spec.DNSProvider != "" {
		solver = "dns01:" + spec.DNSProvider
	}
	return map[string]interface{}{
		"kind":   spec.Kind,
		"solver": solver,
		"email":  spec.Email,
	}
}
