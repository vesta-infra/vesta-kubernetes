package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Mirrors what actually happens in production: instance A seals, the envelope is
// serialised to JSON by the API, written to a file by the CLI, read back on another
// machine, and posted to instance B. A json tag mismatch anywhere in that chain would
// pass the in-memory round-trip test and fail only here.
func TestBundleSurvivesJSONFileRoundTrip(t *testing.T) {
	instanceB, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generating B's identity: %v", err)
	}

	// Instance A only ever has B's public key, in its text form.
	peer, err := ParsePublicKey(FormatPublicKey(instanceB.PublicKey()))
	if err != nil {
		t.Fatalf("parsing B's key: %v", err)
	}

	sealed, err := Seal(peer, &Payload{
		Project: ProjectEntry{Name: "acme", Spec: map[string]interface{}{"displayName": "Acme"}},
		Apps:    []AppEntry{{Name: "api", Spec: map[string]interface{}{"project": "acme"}}},
		Secrets: map[string]map[string]map[string]string{"api": {"prod": {"DATABASE_URL": "postgres://x"}}},
		EnvVars: map[string]map[string]map[string]string{"api": {"prod": {"LOG_LEVEL": "info"}}},
	})
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	path := filepath.Join(t.TempDir(), "acme.bundle.json")
	encoded, _ := json.MarshalIndent(sealed, "", "  ")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("writing bundle: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var received Envelope
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("decoding bundle from disk: %v", err)
	}

	opened, err := Open(instanceB, &received)
	if err != nil {
		t.Fatalf("instance B could not open the bundle it was sealed for: %v", err)
	}
	if opened.Project.Name != "acme" {
		t.Errorf("project = %q, want acme", opened.Project.Name)
	}
	if opened.Secrets["api"]["prod"]["DATABASE_URL"] != "postgres://x" {
		t.Errorf("secret lost in transit: %#v", opened.Secrets)
	}
	if opened.EnvVars["api"]["prod"]["LOG_LEVEL"] != "info" {
		t.Errorf("env var lost in transit: %#v", opened.EnvVars)
	}
	if opened.Apps[0].Spec["project"] != "acme" {
		t.Errorf("app spec lost in transit: %#v", opened.Apps)
	}
}
