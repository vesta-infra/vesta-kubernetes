package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// hostPattern is a conservative DNS name check. Deliberately strict: this hostname ends
// up in an Ingress rule and an ACME order, and a malformed one fails somewhere far from
// where it was typed.
var hostPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`)

// uiIngressComponent is the label the chart stamps on the dashboard's Ingress.
const uiIngressComponent = "ui"

// UIDomainSettings is what the dashboard shows and submits.
type UIDomainSettings struct {
	Host             string `json:"host"`
	TLS              bool   `json:"tls"`
	ClusterIssuer    string `json:"clusterIssuer"`
	IngressClassName string `json:"ingressClassName"`
}

// findUIIngress locates the dashboard's own Ingress by label.
//
// By label rather than name: the object is "<release>-ui", so an install created with
// `helm install myvesta` has a different name. Anything other than exactly one match is
// reported rather than guessed at, the same shape as observedComponents.
func (h *Handler) findUIIngress(ctx context.Context) (*unstructured.Unstructured, error) {
	ns := ReleaseNamespace()
	selector := "app.kubernetes.io/part-of=vesta,app.kubernetes.io/component=" + uiIngressComponent
	list, err := h.K8s.ListResources(ctx, k8s.IngressGVR, ns, selector)
	if err != nil {
		return nil, fmt.Errorf("listing the dashboard ingress: %w", err)
	}
	switch len(list.Items) {
	case 0:
		return nil, nil
	case 1:
		return &list.Items[0], nil
	default:
		return nil, fmt.Errorf("found %d ingresses labelled as the dashboard in %s; refusing to guess which to change", len(list.Items), ns)
	}
}

// GetUIDomain reports how the dashboard is currently exposed.
func (h *Handler) GetUIDomain(c *gin.Context) {
	ctx := c.Request.Context()

	ing, err := h.findUIIngress(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	settings := UIDomainSettings{ClusterIssuer: h.instanceDefaultIssuer(ctx)}
	certReady, certMessage := false, ""

	if ing != nil {
		settings = readIngressSettings(ing)
		if settings.TLS {
			certReady, certMessage = h.certificateStatus(ctx, ing)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"configured":  ing != nil,
		"settings":    settings,
		"certReady":   certReady,
		"certMessage": certMessage,
		"namespace":   ReleaseNamespace(),
		// The chart re-renders this Ingress from values on every upgrade, so a change
		// made here is reverted unless the same values are given to Helm. Rather than
		// pretend otherwise, hand the caller the flags that make it stick.
		"helmValues": helmFlagsFor(settings),
	})
}

// certificateStatus reports whether cert-manager has issued for this Ingress yet.
func (h *Handler) certificateStatus(ctx context.Context, ing *unstructured.Unstructured) (bool, string) {
	tls, found, _ := unstructured.NestedSlice(ing.Object, "spec", "tls")
	if !found || len(tls) == 0 {
		return false, ""
	}
	entry, ok := tls[0].(map[string]interface{})
	if !ok {
		return false, ""
	}
	secretName := getNestedString(entry, "secretName")
	if secretName == "" {
		return false, ""
	}

	cert, err := h.K8s.GetResource(ctx, k8s.CertificateGVR, ing.GetNamespace(), secretName)
	if err != nil {
		// No Certificate object yet is the normal state moments after a change, not an
		// error worth surfacing as one.
		return false, "no certificate issued yet"
	}

	conditions, _, _ := unstructured.NestedSlice(cert.Object, "status", "conditions")
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok || getNestedString(cond, "type") != "Ready" {
			continue
		}
		if getNestedString(cond, "status") == "True" {
			return true, "certificate issued"
		}
		return false, getNestedString(cond, "message")
	}
	return false, "certificate pending"
}

func readIngressSettings(ing *unstructured.Unstructured) UIDomainSettings {
	s := UIDomainSettings{}

	rules, _, _ := unstructured.NestedSlice(ing.Object, "spec", "rules")
	if len(rules) > 0 {
		if rule, ok := rules[0].(map[string]interface{}); ok {
			s.Host = getNestedString(rule, "host")
		}
	}

	tls, _, _ := unstructured.NestedSlice(ing.Object, "spec", "tls")
	s.TLS = len(tls) > 0

	s.IngressClassName, _, _ = unstructured.NestedString(ing.Object, "spec", "ingressClassName")
	s.ClusterIssuer = ing.GetAnnotations()["cert-manager.io/cluster-issuer"]
	return s
}

// helmFlagsFor renders the --set flags that persist a change across `helm upgrade`.
func helmFlagsFor(s UIDomainSettings) []string {
	flags := []string{
		"--set ui.ingress.enabled=true",
		fmt.Sprintf("--set ui.ingress.host=%s", s.Host),
		fmt.Sprintf("--set ui.ingress.tls=%t", s.TLS),
	}
	if s.TLS && s.ClusterIssuer != "" {
		flags = append(flags, fmt.Sprintf("--set ui.ingress.clusterIssuer=%s", s.ClusterIssuer))
	}
	if s.IngressClassName != "" {
		flags = append(flags, fmt.Sprintf("--set ui.ingress.ingressClassName=%s", s.IngressClassName))
	}
	return flags
}

// UpdateUIDomain changes the hostname and certificate issuer of the dashboard itself.
func (h *Handler) UpdateUIDomain(c *gin.Context) {
	var req UIDomainSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	req.Host = strings.ToLower(strings.TrimSpace(req.Host))
	if !hostPattern.MatchString(req.Host) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: fmt.Sprintf("%q is not a valid hostname", req.Host),
		})
		return
	}

	ctx := c.Request.Context()

	// An issuer that does not exist produces an Ingress that never gets a certificate,
	// and cert-manager reports that far from here. Check it while we can still say so.
	if req.TLS && req.ClusterIssuer != "" {
		if _, err := h.K8s.GetClusterResource(ctx, k8s.ClusterIssuerGVR, req.ClusterIssuer); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Code:    400,
				Message: fmt.Sprintf("no ClusterIssuer named %q; create it under SSL Certificates first", req.ClusterIssuer),
			})
			return
		}
	}

	ing, err := h.findUIIngress(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if ing == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: "the dashboard has no ingress yet",
			Details: "enable it once with: helm upgrade ... --set ui.ingress.enabled=true --set ui.ingress.host=" + req.Host,
		})
		return
	}

	previous := readIngressSettings(ing)
	// Read the backend and TLS secret off the live object rather than reconstructing
	// them. They match the ingress name in the stock chart, but that is a coincidence of
	// the templates, not a contract -- and a wrong service name produces an Ingress that
	// routes nowhere while looking entirely healthy.
	backend, secretName := existingBackend(ing)
	patch := buildUIIngressPatch(backend, secretName, req)
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if _, err := h.K8s.PatchResource(ctx, k8s.IngressGVR, ing.GetNamespace(), ing.GetName(), patchBytes); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"settings":   req,
		"previous":   previous,
		"helmValues": helmFlagsFor(req),
		"note":       "the certificate can take a minute to issue; the old hostname stops working immediately",
	})

	h.auditLog(c, "ui_domain_update", "ingress", ing.GetName(), ing.GetName(), "", "",
		map[string]interface{}{"from": previous.Host, "to": req.Host, "clusterIssuer": req.ClusterIssuer})
}

// buildUIIngressPatch produces the merge patch for a hostname and issuer change.
//
// The rules and tls lists are sent whole. PatchResource uses a JSON merge patch, which
// replaces arrays outright rather than merging them, so a partial list would silently
// drop the backend and leave an Ingress that routes nowhere.
// existingBackend returns the service the dashboard ingress currently points at, and the
// TLS secret it currently uses, falling back to the chart's naming when absent.
func existingBackend(ing *unstructured.Unstructured) (service string, secret string) {
	service = ing.GetName()
	secret = ing.GetName() + "-tls"

	rules, _, _ := unstructured.NestedSlice(ing.Object, "spec", "rules")
	if len(rules) > 0 {
		if rule, ok := rules[0].(map[string]interface{}); ok {
			paths, _, _ := unstructured.NestedSlice(rule, "http", "paths")
			if len(paths) > 0 {
				if path, ok := paths[0].(map[string]interface{}); ok {
					if n, found, _ := unstructured.NestedString(path, "backend", "service", "name"); found && n != "" {
						service = n
					}
				}
			}
		}
	}

	tls, _, _ := unstructured.NestedSlice(ing.Object, "spec", "tls")
	if len(tls) > 0 {
		if entry, ok := tls[0].(map[string]interface{}); ok {
			if n := getNestedString(entry, "secretName"); n != "" {
				secret = n
			}
		}
	}
	return service, secret
}

func buildUIIngressPatch(service, secretName string, s UIDomainSettings) map[string]interface{} {
	spec := map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{
				"host": s.Host,
				"http": map[string]interface{}{
					"paths": []interface{}{
						map[string]interface{}{
							"path":     "/",
							"pathType": "Prefix",
							"backend": map[string]interface{}{
								"service": map[string]interface{}{
									"name": service,
									"port": map[string]interface{}{"number": int64(80)},
								},
							},
						},
					},
				},
			},
		},
	}

	if s.TLS {
		spec["tls"] = []interface{}{
			map[string]interface{}{
				"hosts":      []interface{}{s.Host},
				"secretName": secretName,
			},
		}
	} else {
		// Explicit null: merge-patch semantics require it to remove a key, and leaving a
		// stale tls block would keep cert-manager ordering for a host we no longer serve.
		spec["tls"] = nil
	}

	annotations := map[string]interface{}{}
	if s.TLS && s.ClusterIssuer != "" {
		annotations["cert-manager.io/cluster-issuer"] = s.ClusterIssuer
	} else {
		annotations["cert-manager.io/cluster-issuer"] = nil
	}

	return map[string]interface{}{
		"metadata": map[string]interface{}{"annotations": annotations},
		"spec":     spec,
	}
}
