package handlers

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The API writes the password Secret and the operator reads it back. They are separate Go
// modules that share no code, connected only by a name and a key -- so a change to either
// side is not a build error, it is a credential the operator cannot resolve and an image
// pull that fails to authenticate with nothing naming the cause.
func TestPasswordSecretNamingMatchesTheOperator(t *testing.T) {
	src, err := os.ReadFile("../../../operator/controllers/registrypassword.go")
	if err != nil {
		t.Skipf("operator source not available: %v", err)
	}
	text := string(src)

	// The key.
	m := regexp.MustCompile(`RegistryPasswordKey = "([^"]+)"`).FindStringSubmatch(text)
	if m == nil {
		t.Fatal("could not find RegistryPasswordKey in the operator; if it was renamed, " +
			"this guard is no longer checking anything")
	}
	if m[1] != RegistryPasswordKey {
		t.Errorf("the operator stores the password under key %q, the API uses %q; "+
			"the operator would find no password and every pull would fail to authenticate",
			m[1], RegistryPasswordKey)
	}

	// The name prefix.
	p := regexp.MustCompile(`return "([^"]*)" \+ vestaSecretName`).FindStringSubmatch(text)
	if p == nil {
		t.Fatal("could not find the operator's password Secret name derivation")
	}
	if got := registryPasswordSecretName("x"); got != p[1]+"x" {
		t.Errorf("the API derives %q and the operator derives %q; a credential created "+
			"through the API would be invisible to the operator", got, p[1]+"x")
	}
}

// The operator migrates plaintext out of the CRD. If the API still wrote it, every new
// credential would put it straight back and the migration would run forever against a
// moving target.
func TestCreateDoesNotWriteAPlaintextPassword(t *testing.T) {
	src, err := os.ReadFile("secrets.go")
	if err != nil {
		t.Fatal(err)
	}

	// The docker config the handler builds must reference a Secret, not carry a password.
	block := between(string(src), "dockerConfig := map[string]interface{}{", "}")
	if block == "" {
		t.Fatal("could not find the dockerConfig literal in CreateRegistrySecret")
	}
	if strings.Contains(block, `"password"`) {
		t.Errorf("CreateRegistrySecret still writes a plaintext password into the CRD:\n%s", block)
	}
	if !strings.Contains(block, "passwordSecretRef") {
		t.Errorf("CreateRegistrySecret does not reference a password Secret:\n%s", block)
	}
}
