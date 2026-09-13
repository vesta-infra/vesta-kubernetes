package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// middlewareTypes are the types the operator knows how to compile. Keeping the list here
// as well as in the CRD enum lets the API reject a bad type with a message naming the
// valid ones, rather than passing it to the API server for a schema error the UI cannot
// render usefully.
var middlewareTypes = map[string]bool{
	"rateLimit": true, "basicAuth": true, "ipAllowList": true, "headers": true,
	"stripPrefix": true, "compress": true, "retry": true, "circuitBreaker": true,
	"buffering": true, "raw": true,
}

// bodyFieldForType maps a type to the spec field carrying its configuration. The operator
// refuses to compile a spec whose body does not match its type; catching it here turns
// that into a 400 at the moment of editing instead of a resource that reports itself
// broken some seconds later.
var bodyFieldForType = map[string]string{
	"rateLimit": "rateLimit", "basicAuth": "basicAuth", "ipAllowList": "ipAllowList",
	"headers": "headers", "stripPrefix": "stripPrefix", "compress": "compress",
	"retry": "retry", "circuitBreaker": "circuitBreaker", "buffering": "buffering",
	"raw": "raw",
}

// dnsName is the subset of names Kubernetes accepts for an object, checked here so the
// error names the field rather than arriving as an API server rejection.
var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type middlewareRequest struct {
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	DisplayName string                 `json:"displayName"`
	Description string                 `json:"description"`
	Project     string                 `json:"project"`
	App         string                 `json:"app"`
	Environment string                 `json:"environment"`
	Config      map[string]interface{} `json:"config"`
}

func (h *Handler) ListMiddlewares(c *gin.Context) {
	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to list middlewares: " + err.Error()})
		return
	}

	project := c.Query("project")
	out := make([]map[string]interface{}, 0, len(list.Items))
	for i := range list.Items {
		item := middlewareToResponse(&list.Items[i])
		// A middleware scoped to one project is not offered to another. An unscoped one
		// is platform-wide and always listed.
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

	c.JSON(http.StatusOK, gin.H{"middlewares": out})
}

func (h *Handler) GetMiddleware(c *gin.Context) {
	obj, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "middleware not found"})
		return
	}
	c.JSON(http.StatusOK, middlewareToResponse(obj))
}

func (h *Handler) CreateMiddleware(c *gin.Context) {
	var req middlewareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body: " + err.Error()})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if !dnsName.MatchString(req.Name) || len(req.Name) > 253 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400,
			Message: "name must be lowercase letters, digits and dashes, starting and ending with a letter or digit"})
		return
	}
	if err := validateMiddlewarePayload(req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if _, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, req.Name); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "a middleware with that name already exists"})
		return
	}

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaMiddleware",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": vestaSystemNS,
		},
		"spec": middlewareSpecFrom(req),
	}

	created, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to create middleware: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, middlewareToResponse(created))
}

func (h *Handler) UpdateMiddleware(c *gin.Context) {
	name := c.Param("name")

	var req middlewareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body: " + err.Error()})
		return
	}
	req.Name = name
	if err := validateMiddlewarePayload(req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	obj, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, name)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "middleware not found"})
		return
	}

	obj.Object["spec"] = middlewareSpecFrom(req)
	updated, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to update middleware: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, middlewareToResponse(updated))
}

// DeleteMiddleware refuses while apps still reference it. Traefik drops an entire router
// whose middleware is missing, so deleting one still in use does not degrade a route --
// it takes the site off the internet.
func (h *Handler) DeleteMiddleware(c *gin.Context) {
	name := c.Param("name")

	users, err := h.appsReferencingMiddleware(c, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if len(users) > 0 && c.Query("force") != "true" {
		c.JSON(http.StatusConflict, gin.H{
			"code":       409,
			"message":    fmt.Sprintf("middleware is still attached to %d app/environment pair(s); detach it first, or retry with ?force=true", len(users)),
			"attachedTo": users,
		})
		return
	}

	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, name); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to delete middleware: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "middleware deleted"})
}

// GetAppMiddlewares returns the ordered middleware list in force for one environment,
// and says whether it was inherited from the app or set on the environment -- the UI
// needs the distinction to show "inherited" rather than implying the env owns the list.
func (h *Handler) GetAppMiddlewares(c *gin.Context) {
	appID := c.Param("appId")
	env := c.Query("environment")
	if env == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment is required"})
		return
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	names, inherited := resolveMiddlewaresForEnv(app.Object, env)
	c.JSON(http.StatusOK, gin.H{
		"middlewares": names,
		"inherited":   inherited,
		"environment": env,
	})
}

// UpdateAppMiddlewares sets the ordered list for one environment. An empty list is stored
// as an empty list rather than removed, because the two mean different things: absent
// inherits the app-level middlewares, empty applies none.
func (h *Handler) UpdateAppMiddlewares(c *gin.Context) {
	appID := c.Param("appId")

	var req struct {
		Environment string   `json:"environment"`
		Middlewares []string `json:"middlewares"`
		Inherit     bool     `json:"inherit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid request body: " + err.Error()})
		return
	}
	if req.Environment == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment is required"})
		return
	}

	// Referencing a middleware that does not exist would take the router down once the
	// annotation is written, so the names are checked before anything is stored.
	for _, name := range req.Middlewares {
		if _, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaMiddlewareGVR, vestaSystemNS, name); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "no such middleware: " + name})
			return
		}
	}
	if dup := firstDuplicate(req.Middlewares); dup != "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "middleware listed twice: " + dup})
		return
	}

	app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "app not found"})
		return
	}

	envs, _, _ := unstructuredNestedSlice(app.Object, "spec", "environments")
	found := false
	for _, e := range envs {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := em["name"].(string); name != req.Environment {
			continue
		}
		found = true

		ingCfg, _ := em["ingress"].(map[string]interface{})
		if ingCfg == nil {
			ingCfg = map[string]interface{}{}
			em["ingress"] = ingCfg
		}

		if req.Inherit {
			// Removing the key is what restores inheritance; storing [] would pin the
			// environment to "no middlewares" instead.
			delete(ingCfg, "middlewares")
		} else {
			refs := make([]interface{}, 0, len(req.Middlewares))
			for _, name := range req.Middlewares {
				refs = append(refs, name)
			}
			ingCfg["middlewares"] = refs
		}
		break
	}
	if !found {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "environment not found on this app: " + req.Environment})
		return
	}

	if err := unstructured.SetNestedSlice(app.Object, envs, "spec", "environments"); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to update app: " + err.Error()})
		return
	}
	if _, err := h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, app); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to update app: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "middlewares updated", "middlewares": req.Middlewares})
}

func (h *Handler) appsReferencingMiddleware(c *gin.Context, name string) ([]string, error) {
	apps, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	var users []string
	for i := range apps.Items {
		app := apps.Items[i].Object
		appName, _, _ := unstructured.NestedString(app, "metadata", "name")
		envs, _, _ := unstructuredNestedSlice(app, "spec", "environments")
		for _, e := range envs {
			em, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			envName, _ := em["name"].(string)
			names, _ := resolveMiddlewaresForEnv(app, envName)
			for _, ref := range names {
				if ref == name {
					users = append(users, appName+"/"+envName)
					break
				}
			}
		}
	}
	sort.Strings(users)
	return users, nil
}

// resolveMiddlewaresForEnv mirrors the operator's resolution exactly: a per-environment
// list replaces the app-level one, including when empty; an absent one inherits. The two
// must agree, or the UI shows something other than what is served.
func resolveMiddlewaresForEnv(app map[string]interface{}, env string) (names []string, inherited bool) {
	envs, _, _ := unstructuredNestedSlice(app, "spec", "environments")
	for _, e := range envs {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := em["name"].(string); name != env {
			continue
		}
		ingCfg, _ := em["ingress"].(map[string]interface{})
		if ingCfg != nil {
			if raw, present := ingCfg["middlewares"]; present {
				return toStringSlice(raw), false
			}
		}
		break
	}

	if ing, ok := app["spec"].(map[string]interface{}); ok {
		if ingCfg, ok := ing["ingress"].(map[string]interface{}); ok {
			return toStringSlice(ingCfg["middlewares"]), true
		}
	}
	return nil, true
}

func toStringSlice(raw interface{}) []string {
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstDuplicate(items []string) string {
	seen := map[string]bool{}
	for _, item := range items {
		if seen[item] {
			return item
		}
		seen[item] = true
	}
	return ""
}

func validateMiddlewarePayload(req middlewareRequest) error {
	if !middlewareTypes[req.Type] {
		valid := make([]string, 0, len(middlewareTypes))
		for t := range middlewareTypes {
			valid = append(valid, t)
		}
		sort.Strings(valid)
		return fmt.Errorf("type must be one of: %s", strings.Join(valid, ", "))
	}

	// compress is the one type whose empty configuration is complete -- Traefik's
	// defaults compress sensibly -- so it is the one type that may arrive with no body.
	if len(req.Config) == 0 && req.Type != "compress" {
		return fmt.Errorf("a %s middleware needs a configuration", req.Type)
	}

	if req.Type == "raw" && len(req.Config) != 1 {
		return fmt.Errorf("a raw middleware must name exactly one Traefik middleware type, found %d", len(req.Config))
	}
	if req.Type == "basicAuth" {
		if secret, _ := req.Config["secretName"].(string); strings.TrimSpace(secret) == "" {
			return fmt.Errorf("basicAuth requires secretName: credentials are read from a Secret, never stored on the middleware")
		}
		// Refuse plaintext credentials outright rather than storing them in a resource
		// that anyone with read access to the type can fetch.
		for _, forbidden := range []string{"users", "password", "passwords"} {
			if _, present := req.Config[forbidden]; present {
				return fmt.Errorf("basicAuth does not accept %q inline; put the htpasswd data in a Secret and reference it with secretName", forbidden)
			}
		}
	}
	return nil
}

func middlewareSpecFrom(req middlewareRequest) map[string]interface{} {
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

	if req.Type == "raw" {
		// The raw body is the Traefik spec itself, stored under "raw" for the operator
		// to pass through untouched.
		spec["raw"] = req.Config
	} else if len(req.Config) > 0 {
		spec[bodyFieldForType[req.Type]] = req.Config
	} else if req.Type == "compress" {
		spec["compress"] = map[string]interface{}{}
	}
	return spec
}

func middlewareToResponse(obj *unstructured.Unstructured) map[string]interface{} {
	name, _, _ := unstructured.NestedString(obj.Object, "metadata", "name")
	spec, _ := obj.Object["spec"].(map[string]interface{})
	status, _ := obj.Object["status"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
	}

	mwType, _ := spec["type"].(string)
	config := map[string]interface{}{}
	if field, ok := bodyFieldForType[mwType]; ok {
		if body, ok := spec[field].(map[string]interface{}); ok {
			config = body
		}
	}

	out := map[string]interface{}{
		"name":         name,
		"type":         mwType,
		"displayName":  spec["displayName"],
		"description":  spec["description"],
		"project":      spec["project"],
		"app":          spec["app"],
		"environment":  spec["environment"],
		"config":       config,
		"ready":        false,
		"reason":       "",
		"appliedCount": 0,
	}
	if status != nil {
		out["ready"], _ = status["ready"].(bool)
		out["reason"], _ = status["reason"].(string)
		if count, ok := status["appliedCount"].(int64); ok {
			out["appliedCount"] = count
		}
		out["appliedNamespaces"] = status["appliedNamespaces"]
	}
	return out
}
