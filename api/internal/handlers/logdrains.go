package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// drainTypes are the destinations the operator knows how to render. Listed here as well as
// in the CRD enum so a bad type is rejected with a message naming the valid ones, rather
// than as a schema error the UI cannot render usefully.
var drainTypes = map[string]bool{
	"http": true, "loki": true, "syslog": true,
	"elasticsearch": true, "datadog": true, "s3": true, "openobserve": true,
	"forward": true,
}

// credentialFields maps a drain type to the config keys that are credentials. Values under
// these keys never reach the CRD: the handler writes them to a Secret and stores a
// reference. A CRD is readable by anyone holding get on the type.
var credentialFields = map[string][]string{
	"http":          {"authHeader"},
	"loki":          {"basicAuth"},
	"elasticsearch": {"basicAuth", "cloudId"},
	"datadog":       {"apiKey"},
	"s3":            {"credentials"},
	"openobserve":   {"credentials"},
	"forward":       {"sharedKey"},
	"syslog":        {},
}

// secretKeysFor names the Secret keys one credential field expands into, and they are the
// environment-variable suffixes the generated collector config references. A field holding
// a pair -- basic auth, an AWS key pair -- is split on the first colon so the UI can keep
// offering a single input.
//
// These names have to match what the renderer emits. They do not match by convention: the
// operator mounts whatever keys the Secret holds under VESTA_DRAIN_<NAME>_<KEY>, so a
// mismatch here produces an empty credential and a delivery failure that reads as the
// destination rejecting the batch.
var secretKeysFor = map[string][]string{
	"authHeader":  {"AUTH"},
	"apiKey":      {"API_KEY"},
	"cloudId":     {"CLOUD_ID"},
	"basicAuth":   {"USER", "PASSWORD"},
	"sharedKey":   {"SHARED_KEY"},
	"credentials": {"USER", "PASSWORD"},
}

// s3CredentialKeys override the pair names for S3, whose config field is also "credentials"
// but whose collector settings are the AWS ones.
var s3CredentialKeys = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}

func secretKeys(drainType, field string) []string {
	if drainType == "s3" && field == "credentials" {
		return s3CredentialKeys
	}
	return secretKeysFor[field]
}

type logDrainRequest struct {
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	DisplayName string                 `json:"displayName"`
	Description string                 `json:"description"`
	Enabled     *bool                  `json:"enabled"`
	Project     string                 `json:"project"`
	App         string                 `json:"app"`
	Environment string                 `json:"environment"`
	Config      map[string]interface{} `json:"config"`
	ConfigRaw   string                 `json:"configRaw"`

	// Credentials are write-only and never returned. Keyed by the credential field name,
	// e.g. {"apiKey": "dd-..."} or {"basicAuth": "user:password"}.
	Credentials map[string]string `json:"credentials"`
}

func (h *Handler) ListLogDrains(c *gin.Context) {
	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to list log drains: " + err.Error()})
		return
	}

	project := c.Query("project")
	out := make([]map[string]interface{}, 0, len(list.Items))
	for i := range list.Items {
		item := logDrainToResponse(&list.Items[i])
		if project != "" {
			if scoped, _ := item["project"].(string); scoped != "" && scoped != project {
				continue
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"])
	})
	c.JSON(http.StatusOK, gin.H{"drains": out})
}

func (h *Handler) GetLogDrain(c *gin.Context) {
	obj, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "log drain not found"})
		return
	}
	c.JSON(http.StatusOK, logDrainToResponse(obj))
}

func (h *Handler) CreateLogDrain(c *gin.Context) {
	var req logDrainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body: " + err.Error()})
		return
	}
	if err := req.resolveRaw(); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if !dnsName.MatchString(req.Name) || len(req.Name) > 253 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400,
			Message: "name must be lowercase letters, digits and dashes, starting and ending with a letter or digit"})
		return
	}
	if err := validateLogDrain(req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if _, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, req.Name); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "a log drain with that name already exists"})
		return
	}

	config, err := h.storeDrainCredentials(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	req.Config = config

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaLogDrain",
		"metadata": map[string]interface{}{
			"name": req.Name, "namespace": vestaSystemNS,
		},
		"spec": logDrainSpecFrom(req),
	}

	created, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, obj)
	if err != nil {
		// The Secret was written first, so a failure here would otherwise strand
		// credentials for a drain that does not exist.
		_ = h.deleteDrainSecret(c.Request.Context(), req.Name)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to create log drain: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, logDrainToResponse(created))
}

func (h *Handler) UpdateLogDrain(c *gin.Context) {
	name := c.Param("name")

	var req logDrainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body: " + err.Error()})
		return
	}
	req.Name = name
	if err := req.resolveRaw(); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	if err := validateLogDrain(req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	obj, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, name)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "log drain not found"})
		return
	}

	config, err := h.storeDrainCredentials(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	req.Config = config

	obj.Object["spec"] = logDrainSpecFrom(req)
	updated, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to update log drain: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, logDrainToResponse(updated))
}

func (h *Handler) DeleteLogDrain(c *gin.Context) {
	name := c.Param("name")
	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaLogDrainGVR, vestaSystemNS, name); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to delete log drain: " + err.Error()})
		return
	}
	if err := h.deleteDrainSecret(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message": "log drain deleted, but its credentials secret could not be removed: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "log drain deleted"})
}

// storeDrainCredentials writes submitted credentials to a Secret and replaces them in the
// config with references. Returns the config that is safe to store on the resource.
func (h *Handler) storeDrainCredentials(ctx context.Context, req logDrainRequest) (map[string]interface{}, error) {
	config := map[string]interface{}{}
	for key, value := range req.Config {
		config[key] = value
	}

	// Anything named as a credential field is stripped from the config regardless of
	// whether a new value was submitted -- a caller cannot smuggle a literal in by putting
	// it under the same key the reference belongs at.
	fields := credentialFields[req.Type]
	data := map[string][]byte{}
	for _, field := range fields {
		delete(config, field)

		value, submitted := req.Credentials[field]
		if !submitted || strings.TrimSpace(value) == "" {
			continue
		}

		keys := secretKeys(req.Type, field)
		switch len(keys) {
		case 2:
			// A pair in one input: "user:password", "accessKey:secretKey". Split on the
			// first colon only, since a password may well contain one.
			left, right, found := strings.Cut(value, ":")
			if !found {
				return nil, fmt.Errorf("%s must be given as two values separated by a colon", field)
			}
			data[keys[0]] = []byte(left)
			data[keys[1]] = []byte(right)
		case 1:
			data[keys[0]] = []byte(value)
		default:
			return nil, fmt.Errorf("no secret keys are defined for %s", field)
		}

		// The reference records that a credential exists; the value reaches the collector
		// through its environment, not through this.
		config[field] = map[string]interface{}{
			"name": drainSecretName(req.Name),
			"key":  keys[0],
		}
	}

	if len(data) == 0 {
		// Editing without resubmitting credentials keeps the existing Secret and its
		// references, which is what lets the UI show a drain without ever holding its key.
		existing, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
			Get(ctx, drainSecretName(req.Name), metav1.GetOptions{})
		if err == nil {
			for _, field := range fields {
				keys := secretKeys(req.Type, field)
				if len(keys) == 0 {
					continue
				}
				if _, present := existing.Data[keys[0]]; present {
					config[field] = map[string]interface{}{
						"name": drainSecretName(req.Name), "key": keys[0],
					}
				}
			}
		}
		return config, nil
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      drainSecretName(req.Name),
			Namespace: vestaSystemNS,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":     "vesta-api",
				"kubernetes.getvesta.sh/log-drain": req.Name,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	secrets := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS)
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("storing drain credentials: %w", err)
		}
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("updating drain credentials: %w", err)
		}
	}
	return config, nil
}

func (h *Handler) deleteDrainSecret(ctx context.Context, drainName string) error {
	err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
		Delete(ctx, drainSecretName(drainName), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func drainSecretName(drainName string) string {
	return "vld-" + drainName + "-credentials"
}

func (r *logDrainRequest) resolveRaw() error {
	if strings.TrimSpace(r.ConfigRaw) == "" {
		return nil
	}
	// Reuses the middleware parser: YAML or JSON, and a whole manifest or a bare body.
	parsed, err := parseRawMiddleware(r.ConfigRaw)
	if err != nil {
		return err
	}
	r.Config = parsed
	return nil
}

func validateLogDrain(req logDrainRequest) error {
	if !drainTypes[req.Type] {
		valid := make([]string, 0, len(drainTypes))
		for t := range drainTypes {
			valid = append(valid, t)
		}
		sort.Strings(valid)
		return fmt.Errorf("type must be one of: %s", strings.Join(valid, ", "))
	}
	if len(req.Config) == 0 && len(req.Credentials) == 0 {
		return fmt.Errorf("a %s drain needs a configuration", req.Type)
	}

	// An environment without a project names nothing: namespaces are "<project>-<env>", so
	// there is no way to resolve which environments are meant.
	if req.Environment != "" && req.Project == "" {
		return fmt.Errorf("environment requires a project")
	}

	switch req.Type {
	case "http":
		uri, _ := req.Config["uri"].(string)
		if strings.TrimSpace(uri) == "" {
			return fmt.Errorf("an http drain needs a uri")
		}
		if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
			return fmt.Errorf("uri must start with http:// or https://")
		}
	case "datadog":
		if strings.TrimSpace(req.Credentials["apiKey"]) == "" {
			if _, hasRef := req.Config["apiKey"]; !hasRef {
				return fmt.Errorf("a datadog drain needs an apiKey")
			}
		}
	case "loki", "syslog", "elasticsearch":
		if host, _ := req.Config["host"].(string); strings.TrimSpace(host) == "" {
			return fmt.Errorf("a %s drain needs a host", req.Type)
		}
	case "s3":
		if bucket, _ := req.Config["bucket"].(string); strings.TrimSpace(bucket) == "" {
			return fmt.Errorf("an s3 drain needs a bucket")
		}
	case "forward":
		if host, _ := req.Config["host"].(string); strings.TrimSpace(host) == "" {
			return fmt.Errorf("a forward drain needs the host of the Fluent Bit or Fluentd to send to")
		}
	case "openobserve":
		endpoint, _ := req.Config["endpoint"].(string)
		if strings.TrimSpace(endpoint) == "" {
			return fmt.Errorf("an openobserve drain needs an endpoint")
		}
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			return fmt.Errorf("endpoint must start with http:// or https://")
		}
		// The path is built from organization and stream; including one here produces a
		// URL like /api/default/vesta/_json appended to it, which 404s.
		if strings.Contains(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/") {
			return fmt.Errorf("endpoint must be the base URL only, without a path")
		}
	}
	return nil
}

// logDrainSpecFrom builds the stored spec key by key. Nothing from the request reaches the
// resource unless it is named here, which is what keeps a credential out of a CRD whatever
// the caller sent.
func logDrainSpecFrom(req logDrainRequest) map[string]interface{} {
	spec := map[string]interface{}{"type": req.Type}

	for key, value := range map[string]string{
		"displayName": req.DisplayName,
		"description": req.Description,
		"project":     req.Project,
		"app":         req.App,
		"environment": req.Environment,
	} {
		if value != "" {
			spec[key] = value
		}
	}
	if req.Enabled != nil {
		spec["enabled"] = *req.Enabled
	}
	if len(req.Config) > 0 {
		spec[req.Type] = req.Config
	}
	return spec
}

func logDrainToResponse(obj *unstructured.Unstructured) map[string]interface{} {
	name, _, _ := unstructured.NestedString(obj.Object, "metadata", "name")
	spec, _ := obj.Object["spec"].(map[string]interface{})
	status, _ := obj.Object["status"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
	}

	drainType, _ := spec["type"].(string)
	config := map[string]interface{}{}
	if body, ok := spec[drainType].(map[string]interface{}); ok {
		config = body
	}

	enabled := true
	if v, ok := spec["enabled"].(bool); ok {
		enabled = v
	}

	out := map[string]interface{}{
		"name":        name,
		"type":        drainType,
		"displayName": spec["displayName"],
		"description": spec["description"],
		"project":     spec["project"],
		"app":         spec["app"],
		"environment": spec["environment"],
		"enabled":     enabled,
		// config carries secret *references*, never values -- the Secret holds those and
		// the API has no endpoint that reads them back.
		"config":           config,
		"ready":            false,
		"reason":           "",
		"recordsDelivered": int64(0),
		"errors":           int64(0),
	}
	if status != nil {
		out["ready"], _ = status["ready"].(bool)
		out["reason"], _ = status["reason"].(string)
		out["scope"], _ = status["scope"].(string)
		out["lastDeliveryAt"], _ = status["lastDeliveryAt"].(string)
		if v, ok := status["recordsDelivered"].(int64); ok {
			out["recordsDelivered"] = v
		}
		if v, ok := status["errors"].(int64); ok {
			out["errors"] = v
		}
	}
	return out
}
