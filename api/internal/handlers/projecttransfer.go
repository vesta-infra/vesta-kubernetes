package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/bundle"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// GetInstanceIdentity publishes this installation's public key so an operator can hand it
// to whoever is exporting a project to them. The public key is not a secret; the matching
// private key never leaves this cluster.
func (h *Handler) GetInstanceIdentity(c *gin.Context) {
	priv, err := h.K8s.InstanceIdentity(c.Request.Context(), vestaSystemNS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"publicKey":   bundle.FormatPublicKey(priv.PublicKey()),
		"fingerprint": bundle.Fingerprint(priv.PublicKey()),
	})
}

// ExportProject seals a whole project — apps, config and every secret — for one specific
// target instance.
//
// The gate matches RevealAppEnvSecretValues deliberately: this bundle contains every
// secret in the project, so obtaining it must not be easier than revealing a single one.
func (h *Handler) ExportProject(c *gin.Context) {
	projectID := c.Param("projectId")

	var req struct {
		RecipientPublicKey string `json:"recipientPublicKey" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	recipient, err := bundle.ParsePublicKey(req.RecipientPublicKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if role, _ := c.Get("role"); role != "admin" {
		ok, err := h.DB.IsProjectOwner(c.Request.Context(), projectID, c.GetString("userId"))
		if err != nil || !ok {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: "insufficient permissions"})
			return
		}
	}

	payload, err := h.collectProject(c, projectID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: err.Error()})
		return
	}

	envelope, err := bundle.Seal(recipient, payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, envelope)

	// Counts and the recipient only — never app names, secret keys or values.
	h.auditLog(c, "export_project", "project", projectID, projectID, projectID, "",
		map[string]interface{}{
			"recipient":       envelope.Recipient,
			"appCount":        len(payload.Apps),
			"secretCount":     countNested(payload.Secrets),
			"envvarCount":     countNested(payload.EnvVars),
			"sharedSecrets":   len(payload.SharedSecrets),
			"registrySecrets": len(payload.RegistrySecrets),
		})
}

// collectProject gathers everything a project needs in order to run somewhere else.
func (h *Handler) collectProject(c *gin.Context, projectID string) (*bundle.Payload, error) {
	ctx := c.Request.Context()

	project, err := h.K8s.GetResource(ctx, k8s.VestaProjectGVR, vestaSystemNS, projectID)
	if err != nil {
		return nil, errors.New("project not found")
	}
	projectSpec, _, _ := unstructuredNestedMap(project.Object, "spec")

	payload := &bundle.Payload{
		Project: bundle.ProjectEntry{Name: projectID, Spec: projectSpec},
		EnvVars: map[string]map[string]map[string]string{},
		Secrets: map[string]map[string]map[string]string{},
	}

	apps, err := h.K8s.ListResources(ctx, k8s.VestaAppGVR, vestaSystemNS,
		"kubernetes.getvesta.sh/project="+projectID)
	if err != nil {
		return nil, fmt.Errorf("listing apps: %w", err)
	}

	appSpecs := make([]map[string]interface{}, 0, len(apps.Items))
	for _, app := range apps.Items {
		appID := app.GetName()
		appSpec, _, _ := unstructuredNestedMap(app.Object, "spec")
		appSpecs = append(appSpecs, appSpec)
		payload.Apps = append(payload.Apps, bundle.AppEntry{Name: appID, Spec: appSpec})

		// An app that inherits its environments from the project declares none of its
		// own. Falling back keeps its config from being silently left behind — an
		// export that quietly drops secrets is worse than one that fails.
		envs := specEnvironmentNames(appSpec)
		if len(envs) == 0 {
			envs = specEnvironmentNames(projectSpec)
		}

		for _, env := range envs {
			namespace := fmt.Sprintf("%s-%s", projectID, env)

			if cm, err := h.K8s.GetResource(ctx, k8s.ConfigMapGVR, namespace, appID+"-envvars"); err == nil {
				if data, _, _ := unstructuredNestedMap(cm.Object, "data"); len(data) > 0 {
					setNested(payload.EnvVars, appID, env, stringMap(data))
				}
			}

			if sec, err := h.K8s.GetResource(ctx, k8s.VestaSecretGVR, namespace, appID+"-secrets"); err == nil {
				spec, _, _ := unstructuredNestedMap(sec.Object, "spec")
				if data, _, _ := unstructuredNestedMap(spec, "data"); len(data) > 0 {
					setNested(payload.Secrets, appID, env, stringMap(data))
				}
			}
		}
	}

	payload.SharedSecrets = h.collectSharedSecrets(c, projectID)
	payload.RegistrySecrets = h.collectRegistrySecrets(c, referencedPullSecrets(projectSpec, appSpecs))
	return payload, nil
}

func (h *Handler) collectSharedSecrets(c *gin.Context, projectID string) []bundle.SharedSecretEntry {
	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, "",
		"kubernetes.getvesta.sh/project="+projectID+",kubernetes.getvesta.sh/shared=true")
	if err != nil {
		return nil
	}

	// A shared secret exists once per environment namespace with identical data, so
	// group by name the way ListSharedSecrets does and carry the data once.
	grouped := map[string]*bundle.SharedSecretEntry{}
	for _, item := range list.Items {
		name := item.GetName()
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")

		env := item.GetLabels()["kubernetes.getvesta.sh/environment"]
		if env == "" {
			ns := item.GetNamespace()
			if prefix := projectID + "-"; len(ns) > len(prefix) {
				env = ns[len(prefix):]
			}
		}

		entry, ok := grouped[name]
		if !ok {
			data, _, _ := unstructuredNestedMap(spec, "data")
			entry = &bundle.SharedSecretEntry{Name: name, Data: stringMap(data)}
			grouped[name] = entry
		}
		if env != "" {
			entry.Environments = append(entry.Environments, env)
		}
	}

	out := make([]bundle.SharedSecretEntry, 0, len(grouped))
	for _, name := range sortedKeys(grouped) {
		sort.Strings(grouped[name].Environments)
		out = append(out, *grouped[name])
	}
	return out
}

// collectRegistrySecrets returns only the pull credentials the project actually
// references. These are instance-level objects, so exporting all of them would hand the
// target every registry credential this Vesta holds.
func (h *Handler) collectRegistrySecrets(c *gin.Context, wanted map[string]bool) []bundle.RegistrySecretEntry {
	if len(wanted) == 0 {
		return nil
	}

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaSecretGVR, vestaSystemNS,
		"kubernetes.getvesta.sh/type=registry")
	if err != nil {
		return nil
	}

	out := make([]bundle.RegistrySecretEntry, 0, len(wanted))
	for _, item := range list.Items {
		if !wanted[item.GetName()] {
			continue
		}
		spec, _, _ := unstructuredNestedMap(item.Object, "spec")
		cfg, _, _ := unstructuredNestedMap(spec, "dockerConfig")
		out = append(out, bundle.RegistrySecretEntry{
			Name:     item.GetName(),
			Registry: getNestedString(cfg, "registry"),
			Username: getNestedString(cfg, "username"),
			Password: getNestedString(cfg, "password"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ImportProject opens a bundle sealed for this instance and recreates the project.
func (h *Handler) ImportProject(c *gin.Context) {
	var req struct {
		Bundle *bundle.Envelope `json:"bundle" binding:"required"`
		As     string           `json:"as,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	priv, err := h.K8s.InstanceIdentity(c.Request.Context(), vestaSystemNS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	payload, err := bundle.Open(priv, req.Bundle)
	switch {
	case errors.Is(err, bundle.ErrWrongRecipient):
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: "this bundle was sealed for a different Vesta instance",
			Details: fmt.Sprintf("sealed for %s, this instance is %s", req.Bundle.Recipient, bundle.Fingerprint(priv.PublicKey())),
		})
		return
	case err != nil:
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	projectID := payload.Project.Name
	if req.As != "" {
		projectID = req.As
	}

	// Every key is checked before anything is written, so a bad bundle is one 400 naming
	// every offender rather than a half-created project.
	if err := validatePayloadSecretKeys(payload); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if _, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaProjectGVR, vestaSystemNS, projectID); err == nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{
			Code:    409,
			Message: fmt.Sprintf("project %q already exists — supply a different name to import under", projectID),
		})
		return
	}

	created, err := h.applyPayload(c, payload, projectID)
	if err != nil {
		// Kubernetes has no transaction. Report what landed rather than attempting a
		// cleanup delete, which could remove more than this import created.
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": fmt.Sprintf("import failed partway through: %v", err),
			"created": created,
			"project": projectID,
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"project":    projectID,
		"importedAs": req.As != "",
		"created":    created,
	})

	h.auditLog(c, "import_project", "project", projectID, projectID, projectID, "",
		map[string]interface{}{"recipient": req.Bundle.Recipient, "created": created})
}

// applyPayload writes the bundle out, in dependency order. It returns what it managed to
// create even on failure so a partial import can be reported precisely.
func (h *Handler) applyPayload(c *gin.Context, payload *bundle.Payload, projectID string) (map[string]int, error) {
	ctx := c.Request.Context()
	created := map[string]int{}

	// 1. Registry credentials first: an app referencing a pull secret that does not exist
	// yet is accepted by the API server but never pulls.
	for _, reg := range payload.RegistrySecrets {
		if _, err := h.K8s.GetResource(ctx, k8s.VestaSecretGVR, vestaSystemNS, reg.Name); err == nil {
			continue // Instance-level and possibly shared with other projects; leave it.
		}
		obj := map[string]interface{}{
			"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
			"kind":       "VestaSecret",
			"metadata": map[string]interface{}{
				"name":      reg.Name,
				"namespace": vestaSystemNS,
				"labels":    map[string]interface{}{"kubernetes.getvesta.sh/type": "registry"},
			},
			"spec": map[string]interface{}{
				"type": "kubernetes.io/dockerconfigjson",
				"dockerConfig": map[string]interface{}{
					"registry": reg.Registry,
					"username": reg.Username,
					"password": reg.Password,
				},
			},
		}
		if _, err := h.K8s.CreateResource(ctx, k8s.VestaSecretGVR, vestaSystemNS, obj); err != nil {
			return created, fmt.Errorf("creating registry secret %q: %w", reg.Name, err)
		}
		created["registrySecrets"]++
	}

	// 2. The project itself.
	projectObj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaProject",
		"metadata": map[string]interface{}{
			"name":      projectID,
			"namespace": vestaSystemNS,
		},
		"spec": payload.Project.Spec,
	}
	if _, err := h.K8s.CreateResource(ctx, k8s.VestaProjectGVR, vestaSystemNS, projectObj); err != nil {
		return created, fmt.Errorf("creating project: %w", err)
	}
	created["projects"]++

	// 3. Namespaces, before anything is written into them.
	for _, env := range specEnvironmentNames(payload.Project.Spec) {
		if err := h.K8s.EnsureNamespace(ctx, fmt.Sprintf("%s-%s", projectID, env)); err != nil {
			return created, err
		}
		created["namespaces"]++
	}

	// 4. Apps. A rename has to be pushed into every app's spec.project, or the operator
	// reconciles them into namespaces belonging to the old name.
	for _, app := range payload.Apps {
		spec := app.Spec
		if spec == nil {
			spec = map[string]interface{}{}
		}
		spec["project"] = projectID

		obj := map[string]interface{}{
			"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
			"kind":       "VestaApp",
			"metadata": map[string]interface{}{
				"name":      app.Name,
				"namespace": vestaSystemNS,
				"labels":    map[string]interface{}{"kubernetes.getvesta.sh/project": projectID},
			},
			"spec": spec,
		}
		if _, err := h.K8s.CreateResource(ctx, k8s.VestaAppGVR, vestaSystemNS, obj); err != nil {
			return created, fmt.Errorf("creating app %q: %w", app.Name, err)
		}
		created["apps"]++

		// An app may declare environments the project does not, so make sure of the
		// namespace here too rather than trusting step 3 to have covered it.
		for _, env := range specEnvironmentNames(spec) {
			if err := h.K8s.EnsureNamespace(ctx, fmt.Sprintf("%s-%s", projectID, env)); err != nil {
				return created, err
			}
		}
	}

	// 5. Per-app-per-environment config.
	for appID, envs := range payload.EnvVars {
		for env, data := range envs {
			namespace := fmt.Sprintf("%s-%s", projectID, env)
			obj := map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      appID + "-envvars",
					"namespace": namespace,
					"labels": map[string]interface{}{
						"app.kubernetes.io/managed-by":       "vesta",
						"kubernetes.getvesta.sh/project":     projectID,
						"kubernetes.getvesta.sh/app":         appID,
						"kubernetes.getvesta.sh/environment": env,
					},
				},
				"data": anyMap(data),
			}
			if _, err := h.K8s.CreateResource(ctx, k8s.ConfigMapGVR, namespace, obj); err != nil {
				return created, fmt.Errorf("creating env vars for %s/%s: %w", appID, env, err)
			}
			created["envvars"] += len(data)
		}
	}

	for appID, envs := range payload.Secrets {
		for env, data := range envs {
			namespace := fmt.Sprintf("%s-%s", projectID, env)
			obj := map[string]interface{}{
				"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
				"kind":       "VestaSecret",
				"metadata": map[string]interface{}{
					"name":      appID + "-secrets",
					"namespace": namespace,
					"labels": map[string]interface{}{
						"kubernetes.getvesta.sh/project":     projectID,
						"kubernetes.getvesta.sh/app":         appID,
						"kubernetes.getvesta.sh/environment": env,
					},
				},
				"spec": map[string]interface{}{
					"type":        "Opaque",
					"project":     projectID,
					"app":         appID,
					"environment": env,
					"data":        anyMap(data),
				},
			}
			if _, err := h.K8s.CreateResource(ctx, k8s.VestaSecretGVR, namespace, obj); err != nil {
				return created, fmt.Errorf("creating secrets for %s/%s: %w", appID, env, err)
			}
			created["secrets"] += len(data)
		}
	}

	// 6. Shared secrets, fanned out per environment the way CreateSharedSecret does.
	for _, shared := range payload.SharedSecrets {
		for _, env := range shared.Environments {
			namespace := fmt.Sprintf("%s-%s", projectID, env)
			if err := h.K8s.EnsureNamespace(ctx, namespace); err != nil {
				return created, err
			}
			obj := map[string]interface{}{
				"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
				"kind":       "VestaSecret",
				"metadata": map[string]interface{}{
					"name":      shared.Name,
					"namespace": namespace,
					"labels": map[string]interface{}{
						"kubernetes.getvesta.sh/project": projectID,
						"kubernetes.getvesta.sh/shared":  "true",
					},
				},
				"spec": map[string]interface{}{
					"type":    "Opaque",
					"project": projectID,
					"data":    anyMap(shared.Data),
				},
			}
			if _, err := h.K8s.CreateResource(ctx, k8s.VestaSecretGVR, namespace, obj); err != nil {
				return created, fmt.Errorf("creating shared secret %q in %q: %w", shared.Name, env, err)
			}
		}
		created["sharedSecrets"]++
	}

	return created, nil
}

// validatePayloadSecretKeys checks every key in the bundle at once so the caller learns
// about all of them from a single response.
func validatePayloadSecretKeys(payload *bundle.Payload) error {
	keys := make([]string, 0)
	for _, envs := range payload.Secrets {
		for _, data := range envs {
			for k := range data {
				keys = append(keys, k)
			}
		}
	}
	for _, envs := range payload.EnvVars {
		for _, data := range envs {
			for k := range data {
				keys = append(keys, k)
			}
		}
	}
	for _, shared := range payload.SharedSecrets {
		for k := range shared.Data {
			keys = append(keys, k)
		}
	}
	return validateSecretKeys(keys)
}

// referencedPullSecrets collects imagePullSecrets named by the project or any of its apps.
func referencedPullSecrets(projectSpec map[string]interface{}, appSpecs []map[string]interface{}) map[string]bool {
	wanted := map[string]bool{}

	collect := func(v interface{}) {
		refs, ok := v.([]interface{})
		if !ok {
			return
		}
		for _, r := range refs {
			if m, ok := r.(map[string]interface{}); ok {
				if name := getNestedString(m, "name"); name != "" {
					wanted[name] = true
				}
			}
		}
	}

	if projectSpec != nil {
		collect(projectSpec["imagePullSecrets"])
	}
	for _, spec := range appSpecs {
		if image, _, _ := unstructuredNestedMap(spec, "image"); image != nil {
			collect(image["imagePullSecrets"])
		}
	}
	return wanted
}

// specEnvironmentNames reads the name of each entry in a spec's environments list. Both
// VestaProjectSpec and VestaAppSpec shape this field the same way.
func specEnvironmentNames(spec map[string]interface{}) []string {
	envs, ok := spec["environments"].([]interface{})
	if !ok {
		return nil
	}
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		if m, ok := e.(map[string]interface{}); ok {
			if name := getNestedString(m, "name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func stringMap(in map[string]interface{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func anyMap(in map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func setNested(m map[string]map[string]map[string]string, app, env string, data map[string]string) {
	if m[app] == nil {
		m[app] = map[string]map[string]string{}
	}
	m[app][env] = data
}

func countNested(m map[string]map[string]map[string]string) int {
	total := 0
	for _, envs := range m {
		for _, data := range envs {
			total += len(data)
		}
	}
	return total
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
