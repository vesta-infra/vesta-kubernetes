// Package registry talks to container registries and normalises the way they are addressed.
//
// The normalisation half exists because of a silent failure. A registry credential stores
// whatever string the user typed, and the operator uses that string verbatim as the key of
// the docker config "auths" map. Kubelet looks that key up by the host of the image it is
// pulling, so a credential saved as "https://harbor.example.com:443" never matches an image
// at "harbor.example.com/lib/app" -- and the only symptom is ImagePullBackOff, which points
// at the image rather than at the credential.
package registry

import (
	"fmt"
	"strings"
)

// Docker Hub is addressed by three different names depending on who is asking, and the
// auths key is a URL where every other registry's is a bare host. None of this generalises,
// so it is enumerated.
const (
	DockerHubAuthsKey = "https://index.docker.io/v1/"
	DockerHubAPI      = "https://registry-1.docker.io"
)

var dockerHubAliases = map[string]bool{
	"docker.io":                   true,
	"index.docker.io":             true,
	"registry-1.docker.io":        true,
	"https://index.docker.io/v1/": true,
	"":                            true, // an image with no host is a Docker Hub image
}

// NormalizeRegistry turns whatever was typed into the two forms that matter: the key the
// docker config must use so kubelet finds the credential, and the base URL to make API
// calls against.
//
// Accepts "harbor.example.com", "https://harbor.example.com/", "harbor.example.com:443",
// "https://index.docker.io/v1/", "docker.io", and the same with a trailing /v1/ or /v2/.
func NormalizeRegistry(input string) (authsKey, apiBase string) {
	s := strings.TrimSpace(input)

	// Check aliases before stripping, so the full Docker Hub URL is recognised as typed.
	if dockerHubAliases[s] {
		return DockerHubAuthsKey, DockerHubAPI
	}

	scheme := "https"
	if i := strings.Index(s, "://"); i >= 0 {
		if strings.EqualFold(s[:i], "http") {
			scheme = "http"
		}
		s = s[i+3:]
	}

	s = strings.Trim(s, "/")
	// Registry roots are often pasted with the API path still attached.
	for _, suffix := range []string{"/v2", "/v1"} {
		s = strings.TrimSuffix(s, suffix)
	}
	s = strings.Trim(s, "/")

	if dockerHubAliases[strings.ToLower(s)] {
		return DockerHubAuthsKey, DockerHubAPI
	}

	// The auths key is host[:port] with no scheme and no path. A path -- a Harbor project,
	// say -- is not part of the credential's identity: kubelet matches on host alone.
	host := s
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)

	// The default port is implicit in the host kubelet derives from an image reference, so
	// carrying it explicitly would stop the key matching.
	host = strings.TrimSuffix(host, ":443")

	return host, scheme + "://" + host
}

// ImageRef is a parsed image reference.
type ImageRef struct {
	Host string // harbor.example.com, or index.docker.io for an unqualified image
	Path string // lib/app
	Tag  string // v1.2.3, defaulting to latest
}

// String renders the reference back.
func (r ImageRef) String() string {
	if r.Host == "" {
		return r.Path + ":" + r.Tag
	}
	return r.Host + "/" + r.Path + ":" + r.Tag
}

// ParseImageRef splits an image reference into host, path and tag.
//
// The tricky part is telling a registry port from a tag, since both follow a colon. The
// rule -- a colon after the last slash is a tag, otherwise it is a port -- is the one
// already used by the version display helper, generalised here so the registry code and the
// credential matching agree with it.
func ParseImageRef(image string) (ImageRef, error) {
	s := strings.TrimSpace(image)
	if s == "" {
		return ImageRef{}, fmt.Errorf("image reference is empty")
	}

	// A digest is not a tag, and splitting on its colon would produce nonsense.
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}

	ref := ImageRef{Tag: "latest"}

	slash := strings.LastIndex(s, "/")
	if colon := strings.LastIndex(s, ":"); colon > slash {
		ref.Tag = s[colon+1:]
		s = s[:colon]
		if ref.Tag == "" {
			return ImageRef{}, fmt.Errorf("image reference %q has an empty tag", image)
		}
	}

	// The first segment is a host only if it looks like one. "acme/web" is a Docker Hub
	// image owned by acme, not a host called acme.
	if i := strings.Index(s, "/"); i >= 0 {
		first := s[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			ref.Host = strings.ToLower(first)
			ref.Path = s[i+1:]
		} else {
			ref.Host = "index.docker.io"
			ref.Path = s
		}
	} else {
		// A bare name is an official Docker Hub image.
		ref.Host = "index.docker.io"
		ref.Path = "library/" + s
	}

	if ref.Path == "" {
		return ImageRef{}, fmt.Errorf("image reference %q has no path", image)
	}
	return ref, nil
}

// CredentialMatchesImage reports whether a stored registry credential would be found by
// kubelet when pulling an image.
//
// This is the check that turns the Harbor failure from an ImagePullBackOff into something
// the UI can say out loud before anything is deployed.
func CredentialMatchesImage(registryValue, image string) (bool, error) {
	ref, err := ParseImageRef(image)
	if err != nil {
		return false, err
	}

	authsKey, _ := NormalizeRegistry(registryValue)
	imageKey, _ := NormalizeRegistry(ref.Host)
	return authsKey == imageKey, nil
}
