package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// Managed add-ons.
//
// spec.addons on an app was accepted and stored for a long time with nothing reconciling it,
// so declaring an add-on did nothing. These endpoints drive the VestaAddon kind that now
// does the work.

// supportedAddonTypes mirrors the operator's engine table. Duplicated deliberately: the API
// should reject an unknown type with a useful message rather than creating a resource the
// operator will later mark failed, and importing the operator module here would be a much
// larger coupling than one list.
var supportedAddonTypes = []string{"postgres", "mysql", "redis", "mongodb"}

func isSupportedAddonType(t string) bool {
	for _, s := range supportedAddonTypes {
		if s == t {
			return true
		}
	}
	return false
}

// addonSecretName is where an add-on's connection details land. It must agree with the
// operator's AddonSecretName.
func addonSecretName(addonName string) string { return addonName + "-credentials" }

// ListAddons returns the add-ons of a project.
func (h *Handler) ListAddons(c *gin.Context) {
	projectID := c.Param("projectId")

	list, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaAddonGVR, vestaSystemNS,
		"kubernetes.getvesta.sh/project="+projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := []gin.H{}
	for _, obj := range list.Items {
		spec, _, _ := unstructuredNestedMap(obj.Object, "spec")
		status, _, _ := unstructuredNestedMap(obj.Object, "status")

		ready, _ := status["ready"].(bool)
		items = append(items, gin.H{
			"name":        obj.GetName(),
			"type":        getNestedString(spec, "type"),
			"version":     getNestedString(spec, "version"),
			"environment": getNestedString(spec, "environment"),
			"size":        getNestedString(spec, "size"),
			"storage":     getNestedString(spec, "storage"),
			"ready":       ready,
			"reason":      getNestedString(status, "reason"),
			"phase":       getNestedString(status, "phase"),
			"secretName":  getNestedString(status, "secretName"),
			"createdAt":   obj.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
		})
	}

	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

// CreateAddon provisions a datastore.
func (h *Handler) CreateAddon(c *gin.Context) {
	projectID := c.Param("projectId")

	var req struct {
		Name           string `json:"name" binding:"required"`
		Type           string `json:"type" binding:"required"`
		Version        string `json:"version"`
		Environment    string `json:"environment"`
		Size           string `json:"size"`
		Storage        string `json:"storage"`
		StorageClass   string `json:"storageClass"`
		DeletionPolicy string `json:"deletionPolicy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if !isSupportedAddonType(req.Type) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400,
			Message: fmt.Sprintf("unsupported type %q; supported: %s",
				req.Type, strings.Join(supportedAddonTypes, ", "))})
		return
	}

	spec := map[string]interface{}{
		"type":    req.Type,
		"project": projectID,
	}
	for key, value := range map[string]string{
		"version":        req.Version,
		"environment":    req.Environment,
		"size":           req.Size,
		"storage":        req.Storage,
		"storageClass":   req.StorageClass,
		"deletionPolicy": req.DeletionPolicy,
	} {
		if value != "" {
			spec[key] = value
		}
	}

	obj := map[string]interface{}{
		"apiVersion": "kubernetes.getvesta.sh/v1alpha1",
		"kind":       "VestaAddon",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": vestaSystemNS,
			"labels": map[string]interface{}{
				"kubernetes.getvesta.sh/project": projectID,
			},
		},
		"spec": spec,
	}

	created, err := h.K8s.CreateResource(c.Request.Context(), k8s.VestaAddonGVR, vestaSystemNS, obj)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "create_addon", "addon", created.GetName(), created.GetName(), projectID, req.Environment,
		map[string]interface{}{"type": req.Type})

	c.JSON(http.StatusCreated, gin.H{
		"name": created.GetName(), "type": req.Type, "project": projectID,
		"secretName": addonSecretName(created.GetName()),
	})
}

// DeleteAddon removes an add-on.
//
// A datastore holds data, so this needs ?force=true. The operator then decides what happens
// to the volume from the add-on's own deletion policy, which retains by default.
func (h *Handler) DeleteAddon(c *gin.Context) {
	projectID := c.Param("projectId")
	name := c.Param("name")

	if c.Query("force") != "true" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400,
			Message: "deleting an add-on removes a running datastore; repeat with ?force=true"})
		return
	}

	if err := h.K8s.DeleteResource(c.Request.Context(), k8s.VestaAddonGVR, vestaSystemNS, name); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "delete_addon", "addon", name, name, projectID, "", nil)
	c.JSON(http.StatusOK, gin.H{"status": "deleting", "name": name})
}

// RevealAddonCredentials returns an add-on's connection details.
//
// Gated exactly like revealing a secret, because that is what it is: the response carries a
// password and a connection URL that contains it.
func (h *Handler) RevealAddonCredentials(c *gin.Context) {
	name := c.Param("name")
	environment := c.Query("environment")
	if environment == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400,
			Message: "environment is required: an add-on has separate credentials per environment"})
		return
	}

	namespace := fmt.Sprintf("%s-%s", c.Param("projectId"), environment)
	secret, err := h.K8s.Clientset.CoreV1().Secrets(namespace).
		Get(c.Request.Context(), addonSecretName(name), metav1.GetOptions{})
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404,
			Message: "no credentials yet; the add-on may still be starting"})
		return
	}

	data := map[string]string{}
	for k, v := range secret.Data {
		data[k] = string(v)
	}

	h.auditLog(c, "reveal_addon_credentials", "addon", name, name, c.Param("projectId"), environment, nil)
	c.JSON(http.StatusOK, gin.H{"name": name, "environment": environment, "credentials": data})
}

// BindAddon attaches an add-on's credentials to an app.
//
// It adds a secretRef to spec.runtime.secrets rather than inventing a binding of its own.
// That path already injects every key of a Secret as environment variables, already scopes
// to named environments, and already feeds the rollout hash -- so a credential change
// restarts the app without anything new being written to make it.
func (h *Handler) BindAddon(c *gin.Context) {
	appID := c.Param("appId")

	var req struct {
		Addon        string   `json:"addon" binding:"required"`
		Environments []string `json:"environments"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	secretName := addonSecretName(req.Addon)

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
		if err != nil {
			return err
		}

		spec, _, _ := unstructuredNestedMap(app.Object, "spec")
		runtimeSpec, _, _ := unstructuredNestedMap(spec, "runtime")
		if runtimeSpec == nil {
			runtimeSpec = map[string]interface{}{}
		}

		secrets, _ := runtimeSpec["secrets"].([]interface{})
		for _, entry := range secrets {
			m, _ := entry.(map[string]interface{})
			ref, _ := m["secretRef"].(map[string]interface{})
			if getNestedString(ref, "name") == secretName {
				return nil // already bound; binding twice would duplicate every env var
			}
		}

		binding := map[string]interface{}{
			"secretRef": map[string]interface{}{"name": secretName},
		}
		if len(req.Environments) > 0 {
			envs := make([]interface{}, 0, len(req.Environments))
			for _, e := range req.Environments {
				envs = append(envs, e)
			}
			binding["environments"] = envs
		}

		runtimeSpec["secrets"] = append(secrets, binding)
		spec["runtime"] = runtimeSpec
		app.Object["spec"] = spec

		_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, app)
		return err
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "bind_addon", "app", appID, appID, "", "",
		map[string]interface{}{"addon": req.Addon})

	c.JSON(http.StatusOK, gin.H{"status": "bound", "app": appID, "addon": req.Addon})
}

// UnbindAddon detaches an add-on's credentials from an app.
//
// The add-on and its data are untouched: this removes the environment variables, not the
// database.
func (h *Handler) UnbindAddon(c *gin.Context) {
	appID := c.Param("appId")
	secretName := addonSecretName(c.Param("name"))

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		app, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, appID)
		if err != nil {
			return err
		}

		spec, _, _ := unstructuredNestedMap(app.Object, "spec")
		runtimeSpec, _, _ := unstructuredNestedMap(spec, "runtime")
		if runtimeSpec == nil {
			return nil
		}

		secrets, _ := runtimeSpec["secrets"].([]interface{})
		kept := make([]interface{}, 0, len(secrets))
		for _, entry := range secrets {
			m, _ := entry.(map[string]interface{})
			ref, _ := m["secretRef"].(map[string]interface{})
			if getNestedString(ref, "name") == secretName {
				continue
			}
			kept = append(kept, entry)
		}

		runtimeSpec["secrets"] = kept
		spec["runtime"] = runtimeSpec
		app.Object["spec"] = spec

		_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaAppGVR, vestaSystemNS, app)
		return err
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "unbind_addon", "app", appID, appID, "", "",
		map[string]interface{}{"addon": c.Param("name")})

	c.JSON(http.StatusOK, gin.H{"status": "unbound", "app": appID})
}
