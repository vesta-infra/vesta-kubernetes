// Package bundle seals a Vesta project — its apps, configuration and every secret they
// depend on — into a file that only one specific Vesta instance can open.
//
// The transport is a human carrying a file, not a network path between clusters, so the
// file has to survive being intercepted, mailed to the wrong person, or sitting in a
// downloads folder. Everything except the recipient's fingerprint is therefore inside the
// ciphertext, including the project and app names.
package bundle

// Version is the envelope format version. It is a plain integer rather than a semver
// string because the only question a reader ever asks is "do I understand this or not".
const Version = 1

// Envelope is the on-disk bundle. Only Recipient is meaningful before decryption: it lets
// an instance say "this was not sealed for me" without burning a decryption attempt, and
// lets a human tell two bundles apart without being able to read either.
type Envelope struct {
	Version            int    `json:"vestaBundle"`
	ExportedAt         string `json:"exportedAt"`
	Recipient          string `json:"recipient"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

// Payload is what the ciphertext contains once opened.
//
// Only spec is carried for the custom resources — never metadata or status — so there are
// no resourceVersion or uid fields for the target's API server to reject, and no status
// from a cluster the target has never seen.
type Payload struct {
	Project ProjectEntry `json:"project"`
	Apps    []AppEntry   `json:"apps,omitempty"`

	// EnvVars and Secrets are keyed app name -> environment name -> key -> value.
	EnvVars map[string]map[string]map[string]string `json:"envvars,omitempty"`
	Secrets map[string]map[string]map[string]string `json:"secrets,omitempty"`

	SharedSecrets   []SharedSecretEntry   `json:"sharedSecrets,omitempty"`
	RegistrySecrets []RegistrySecretEntry `json:"registrySecrets,omitempty"`
}

type ProjectEntry struct {
	Name string                 `json:"name"`
	Spec map[string]interface{} `json:"spec"`
}

type AppEntry struct {
	Name string                 `json:"name"`
	Spec map[string]interface{} `json:"spec"`
}

type SharedSecretEntry struct {
	Name         string            `json:"name"`
	Environments []string          `json:"environments,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
}

// RegistrySecretEntry carries image-pull credentials. These live in vesta-system and are
// instance-level rather than project-scoped, but a project whose images cannot be pulled
// has not really been transferred, so referenced ones travel with the bundle.
type RegistrySecretEntry struct {
	Name     string `json:"name"`
	Registry string `json:"registry"`
	Username string `json:"username"`
	Password string `json:"password"`
}
