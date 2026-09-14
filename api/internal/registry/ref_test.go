package registry

import "testing"

// The production failure: whatever the user typed became the key of the docker config
// "auths" map verbatim. Kubelet looks that key up by the image's host, so a Harbor
// credential saved as "https://harbor.example.com:443" never matched an image at
// "harbor.example.com/lib/app" -- and the only symptom was ImagePullBackOff, which points
// at the image rather than the credential.
func TestNormalizeRegistry(t *testing.T) {
	cases := []struct {
		input    string
		wantKey  string
		wantBase string
	}{
		// The Harbor case, in every form someone might paste.
		{"harbor.example.com", "harbor.example.com", "https://harbor.example.com"},
		{"https://harbor.example.com", "harbor.example.com", "https://harbor.example.com"},
		{"https://harbor.example.com/", "harbor.example.com", "https://harbor.example.com"},
		{"https://harbor.example.com:443", "harbor.example.com", "https://harbor.example.com"},
		{"https://harbor.example.com/v2/", "harbor.example.com", "https://harbor.example.com"},
		{"HARBOR.EXAMPLE.COM", "harbor.example.com", "https://harbor.example.com"},

		// A non-default port is part of the host and must survive.
		{"registry.internal:5000", "registry.internal:5000", "https://registry.internal:5000"},
		{"http://registry.internal:5000", "registry.internal:5000", "http://registry.internal:5000"},

		// A path is not part of the credential's identity -- kubelet matches on host.
		{"harbor.example.com/library", "harbor.example.com", "https://harbor.example.com"},

		// Docker Hub answers to three names and its auths key is a URL, unlike every other
		// registry's bare host. None of that generalises.
		{"docker.io", DockerHubAuthsKey, DockerHubAPI},
		{"index.docker.io", DockerHubAuthsKey, DockerHubAPI},
		{"registry-1.docker.io", DockerHubAuthsKey, DockerHubAPI},
		{"https://index.docker.io/v1/", DockerHubAuthsKey, DockerHubAPI},
		{"", DockerHubAuthsKey, DockerHubAPI},

		{"ghcr.io", "ghcr.io", "https://ghcr.io"},
		{"  ghcr.io  ", "ghcr.io", "https://ghcr.io"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			key, base := NormalizeRegistry(tc.input)
			if key != tc.wantKey {
				t.Errorf("auths key = %q, want %q", key, tc.wantKey)
			}
			if base != tc.wantBase {
				t.Errorf("api base = %q, want %q", base, tc.wantBase)
			}
		})
	}
}

// Every form of one registry must produce one key, or two credentials for the same server
// look like credentials for two servers.
func TestNormalizeRegistryIsStable(t *testing.T) {
	groups := [][]string{
		{"harbor.example.com", "https://harbor.example.com", "https://harbor.example.com:443/", "HARBOR.EXAMPLE.COM/v2/"},
		{"docker.io", "index.docker.io", "https://index.docker.io/v1/", "registry-1.docker.io"},
	}
	for _, group := range groups {
		want, _ := NormalizeRegistry(group[0])
		for _, form := range group[1:] {
			if got, _ := NormalizeRegistry(form); got != want {
				t.Errorf("NormalizeRegistry(%q) = %q, want %q -- same registry, different key", form, got, want)
			}
		}
	}
}

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		image string
		want  ImageRef
	}{
		{"harbor.example.com/lib/app:v1", ImageRef{"harbor.example.com", "lib/app", "v1"}},
		{"harbor.example.com/lib/app", ImageRef{"harbor.example.com", "lib/app", "latest"}},

		// The one that needs care: a colon before the last slash is a port, not a tag.
		{"registry:5000/vesta/api", ImageRef{"registry:5000", "vesta/api", "latest"}},
		{"registry:5000/vesta/api:v2", ImageRef{"registry:5000", "vesta/api", "v2"}},

		// No dot or colon in the first segment means it is an owner, not a host.
		{"acme/web:v1", ImageRef{"index.docker.io", "acme/web", "v1"}},
		{"nginx", ImageRef{"index.docker.io", "library/nginx", "latest"}},
		{"nginx:1.25", ImageRef{"index.docker.io", "library/nginx", "1.25"}},

		{"localhost:5000/app:dev", ImageRef{"localhost:5000", "app", "dev"}},
		{"localhost/app", ImageRef{"localhost", "app", "latest"}},

		// A digest is not a tag; splitting on its colon would produce nonsense.
		{"harbor.example.com/lib/app@sha256:abc123", ImageRef{"harbor.example.com", "lib/app", "latest"}},

		{"ghcr.io/org/repo:sha-abc1234", ImageRef{"ghcr.io", "org/repo", "sha-abc1234"}},
	}

	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			got, err := ParseImageRef(tc.image)
			if err != nil {
				t.Fatalf("ParseImageRef(%q): %v", tc.image, err)
			}
			if got != tc.want {
				t.Errorf("ParseImageRef(%q) = %+v, want %+v", tc.image, got, tc.want)
			}
		})
	}
}

func TestParseImageRefRejects(t *testing.T) {
	for _, image := range []string{"", "   ", "app:"} {
		if got, err := ParseImageRef(image); err == nil {
			t.Errorf("ParseImageRef(%q) = %+v, want an error", image, got)
		}
	}
}

// This is the check that turns the Harbor failure into something the UI can say before
// anything is deployed, instead of an ImagePullBackOff afterwards.
func TestCredentialMatchesImage(t *testing.T) {
	cases := []struct {
		name     string
		registry string
		image    string
		want     bool
	}{
		{
			"the Harbor mismatch, now recognised as a match",
			"https://harbor.example.com:443", "harbor.example.com/lib/app:v1", true,
		},
		{"plain host", "harbor.example.com", "harbor.example.com/lib/app:v1", true},
		{"different host", "harbor.example.com", "other.example.com/lib/app:v1", false},
		{"docker hub credential, docker hub image", "https://index.docker.io/v1/", "acme/web:v1", true},
		{"docker hub credential, official image", "docker.io", "nginx", true},
		{"docker hub credential, private registry image", "docker.io", "harbor.example.com/lib/app", false},
		{"port matters when it is not 443", "registry.internal:5000", "registry.internal:5000/app", true},
		{"a credential for the wrong port does not match", "registry.internal:5000", "registry.internal/app", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CredentialMatchesImage(tc.registry, tc.image)
			if err != nil {
				t.Fatalf("CredentialMatchesImage: %v", err)
			}
			if got != tc.want {
				t.Errorf("CredentialMatchesImage(%q, %q) = %v, want %v",
					tc.registry, tc.image, got, tc.want)
			}
		})
	}
}
