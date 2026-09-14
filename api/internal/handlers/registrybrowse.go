package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/rbac"
	"kubernetes.getvesta.sh/api/internal/registry"
)

// Browsing a registry through a stored credential.
//
// Image repositories and tags used to be free text on six screens, with nothing checking
// that what was typed existed. The only feedback was ImagePullBackOff after a deploy, which
// names the image rather than the mistake.

// credentialsFor loads a registry credential by name and returns it in the form the
// registry client wants. The password is read here and never leaves this process.
func (h *Handler) credentialsFor(c *gin.Context, name string) (registry.Credentials, bool) {
	obj, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaSecretGVR, vestaSystemNS, name)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "registry credential not found"})
		return registry.Credentials{}, false
	}

	spec, _, _ := unstructuredNestedMap(obj.Object, "spec")

	// Scope is checked here rather than in each of the three browse endpoints, because this
	// is the one place all of them load a credential -- a check added per-endpoint is a
	// check somebody forgets to add to the fourth.
	//
	// 404 rather than 403: a caller who may not use a credential should not learn that it
	// exists, and this matches the genuinely-missing case exactly.
	if !h.mayAccessSecret(c, spec, rbac.ActionRead) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "registry credential not found"})
		return registry.Credentials{}, false
	}

	dockerConfig, _, _ := unstructuredNestedMap(spec, "dockerConfig")
	if dockerConfig == nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code: 400, Message: "that secret is not a registry credential"})
		return registry.Credentials{}, false
	}

	return registry.Credentials{
		Registry: getNestedString(dockerConfig, "registry"),
		Username: getNestedString(dockerConfig, "username"),
		// Resolved from the Secret the credential references, falling back to the
		// plaintext field for anything the operator has not migrated yet.
		Password: h.readRegistryPassword(c.Request.Context(), dockerConfig),
		Flavor:   getNestedString(dockerConfig, "flavor"),
	}, true
}

// ListRegistryRepositories returns the repositories a credential can see.
func (h *Handler) ListRegistryRepositories(c *gin.Context) {
	creds, ok := h.credentialsFor(c, c.Param("name"))
	if !ok {
		return
	}

	repos, err := h.Registry.ListRepositories(c.Request.Context(), creds)
	if err != nil {
		// Reported rather than swallowed into an empty list. A picker that cannot tell
		// "no repositories" from "the call failed" leaves the user retyping a value that
		// was never going to be offered.
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"repositories": repos, "flavor": registryFlavor(creds)})
}

// ListRegistryTags returns the tags of one repository.
func (h *Handler) ListRegistryTags(c *gin.Context) {
	repository := c.Query("repository")
	if repository == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "repository is required"})
		return
	}

	creds, ok := h.credentialsFor(c, c.Param("name"))
	if !ok {
		return
	}

	// The UI holds a full image reference, the registry API wants a path with no host. The
	// stripping happens here rather than in the browser so the rule that decides whether a
	// leading segment is a host or an owner exists once, in Go, next to the parser the rest
	// of the platform uses.
	if ref, err := registry.ParseImageRef(repository); err == nil {
		repository = ref.Path
	}

	tags, err := h.Registry.ListTags(c.Request.Context(), creds, repository)
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

// TestRegistryCredential checks a credential against its registry.
//
// The response also reports the normalised auths key, because a credential can authenticate
// perfectly and still never be used: kubelet looks the key up by the image's host, and a
// registry typed as a URL with a port produces a key no image will ever match.
func (h *Handler) TestRegistryCredential(c *gin.Context) {
	creds, ok := h.credentialsFor(c, c.Param("name"))
	if !ok {
		return
	}

	authsKey, apiBase := registry.NormalizeRegistry(creds.Registry)
	result := gin.H{
		"registry": creds.Registry,
		"authsKey": authsKey,
		"apiBase":  apiBase,
		"flavor":   registryFlavor(creds),
		// True when the stored value already normalises to itself, which is what kubelet
		// will look for.
		"normalized": creds.Registry == authsKey,
	}

	if err := h.Registry.Ping(c.Request.Context(), creds); err != nil {
		result["ok"] = false
		result["error"] = err.Error()
		c.JSON(http.StatusOK, result)
		return
	}

	result["ok"] = true
	c.JSON(http.StatusOK, result)
}

func registryFlavor(creds registry.Credentials) string {
	if creds.Flavor != "" {
		return creds.Flavor
	}
	return registry.DetectFlavor(creds.Registry)
}
