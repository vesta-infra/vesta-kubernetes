package handlers

import (
	"strings"
	"testing"

	"kubernetes.getvesta.sh/api/internal/bundle"
)

func TestSpecEnvironmentNamesReadsEveryEntry(t *testing.T) {
	spec := map[string]interface{}{
		"environments": []interface{}{
			map[string]interface{}{"name": "staging"},
			map[string]interface{}{"name": "production", "branch": "main"},
		},
	}
	got := specEnvironmentNames(spec)
	if len(got) != 2 || got[0] != "staging" || got[1] != "production" {
		t.Fatalf("environments = %v, want [staging production]", got)
	}
}

func TestSpecEnvironmentNamesToleratesMissingField(t *testing.T) {
	// An app created before environments existed has no such key; export must not panic.
	if got := specEnvironmentNames(map[string]interface{}{"project": "acme"}); len(got) != 0 {
		t.Fatalf("environments = %v, want empty", got)
	}
}

// Exporting every registry credential would hand the target instance keys to registries
// the project never used.
func TestReferencedPullSecretsCollectsOnlyWhatIsUsed(t *testing.T) {
	projectSpec := map[string]interface{}{
		"imagePullSecrets": []interface{}{map[string]interface{}{"name": "ghcr"}},
	}
	appSpecs := []map[string]interface{}{
		{"image": map[string]interface{}{
			"imagePullSecrets": []interface{}{map[string]interface{}{"name": "quay"}},
		}},
		{"image": map[string]interface{}{
			"imagePullSecrets": []interface{}{map[string]interface{}{"name": "ghcr"}},
		}},
	}

	got := referencedPullSecrets(projectSpec, appSpecs)
	if len(got) != 2 || !got["ghcr"] || !got["quay"] {
		t.Fatalf("pull secrets = %v, want ghcr and quay only", got)
	}
	if got["dockerhub"] {
		t.Error("collected a credential the project never referenced")
	}
}

func TestReferencedPullSecretsIsEmptyWhenNoneDeclared(t *testing.T) {
	got := referencedPullSecrets(map[string]interface{}{}, []map[string]interface{}{{"project": "acme"}})
	if len(got) != 0 {
		t.Fatalf("pull secrets = %v, want none", got)
	}
}

// One 400 naming every bad key, rather than a project half-created before the failure.
func TestValidatePayloadSecretKeysReportsEveryOffenderAtOnce(t *testing.T) {
	payload := &bundle.Payload{
		Secrets: map[string]map[string]map[string]string{
			"api": {"staging": {"good": "1", "bad key": "2"}},
		},
		EnvVars: map[string]map[string]map[string]string{
			"api": {"staging": {"also$bad": "3"}},
		},
		SharedSecrets: []bundle.SharedSecretEntry{
			{Name: "stripe", Data: map[string]string{"third bad": "4"}},
		},
	}

	err := validatePayloadSecretKeys(payload)
	if err == nil {
		t.Fatal("expected invalid keys to be rejected")
	}
	for _, want := range []string{"bad key", "also$bad", "third bad"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

func TestValidatePayloadSecretKeysAcceptsCleanBundle(t *testing.T) {
	payload := &bundle.Payload{
		Secrets: map[string]map[string]map[string]string{
			"api": {"staging": {"DATABASE_URL": "x", "API-KEY": "y", "a.b": "z"}},
		},
	}
	if err := validatePayloadSecretKeys(payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCountNestedSumsAcrossAppsAndEnvironments(t *testing.T) {
	m := map[string]map[string]map[string]string{
		"api": {"staging": {"A": "1", "B": "2"}, "production": {"A": "1"}},
		"web": {"staging": {"C": "3"}},
	}
	if got := countNested(m); got != 4 {
		t.Fatalf("count = %d, want 4", got)
	}
}
