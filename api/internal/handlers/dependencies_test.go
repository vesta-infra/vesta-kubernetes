package handlers

import "testing"

// The dependency graph read spec.runtime.envVars as a map. VestaApp has no such field --
// the environment is spec.runtime.env, a list of {name, value} -- so the read always came
// back empty and the graph drew every app as an island.
//
// Nothing failed. The endpoint returned 200 with nodes and no edges, which is exactly what
// a project with no dependencies looks like, so the feature appeared to work for anyone who
// did not already know their apps referenced each other.
func TestAppEnvPairsReadsTheFieldThatExists(t *testing.T) {
	spec := map[string]interface{}{
		"runtime": map[string]interface{}{
			"env": []interface{}{
				map[string]interface{}{"name": "DATABASE_URL", "value": "postgres://db.acme-prod:5432/app"},
				map[string]interface{}{"name": "API_BASE", "value": "http://billing.acme-prod"},
			},
		},
	}

	got := appEnvPairs(spec)
	if len(got) != 2 {
		t.Fatalf("read %d variables from spec.runtime.env, want 2: %v", len(got), got)
	}
	if got["DATABASE_URL"] != "postgres://db.acme-prod:5432/app" {
		t.Errorf("DATABASE_URL = %q", got["DATABASE_URL"])
	}

	// Negative control: the field the old code asked for must genuinely not work, or this
	// test would have passed against the bug.
	wrong := map[string]interface{}{
		"runtime": map[string]interface{}{
			"envVars": map[string]interface{}{"DATABASE_URL": "postgres://db"},
		},
	}
	if len(appEnvPairs(wrong)) != 0 {
		t.Error("appEnvPairs read spec.runtime.envVars, which does not exist on VestaApp")
	}
}

// A variable sourced from a Secret carries valueFrom and no literal value. There is nothing
// to match against, and resolving it would mean reading secret material to draw a diagram.
func TestAppEnvPairsSkipsWhatItCannotRead(t *testing.T) {
	spec := map[string]interface{}{
		"runtime": map[string]interface{}{
			"env": []interface{}{
				map[string]interface{}{"name": "PLAIN", "value": "http://web.acme-prod"},
				map[string]interface{}{
					"name": "SECRET_URL",
					"valueFrom": map[string]interface{}{
						"secretKeyRef": map[string]interface{}{"name": "db", "key": "url"},
					},
				},
				map[string]interface{}{"name": "EMPTY", "value": ""},
				map[string]interface{}{"value": "no name at all"},
				"not a map",
			},
		},
	}

	got := appEnvPairs(spec)
	if len(got) != 1 || got["PLAIN"] == "" {
		t.Errorf("= %v, want only PLAIN", got)
	}
	if _, ok := got["SECRET_URL"]; ok {
		t.Error("a secret-sourced variable was included; its value is not in the spec to read")
	}
}

// Missing and malformed shapes must produce an empty map rather than panicking: this runs
// over whatever is stored in the cluster, including objects written by older releases.
func TestAppEnvPairsHandlesAbsentAndMalformedSpecs(t *testing.T) {
	cases := []struct {
		name string
		spec map[string]interface{}
	}{
		{"nil spec", nil},
		{"no runtime", map[string]interface{}{}},
		{"runtime is not a map", map[string]interface{}{"runtime": "nonsense"}},
		{"env is not a list", map[string]interface{}{"runtime": map[string]interface{}{"env": "nonsense"}}},
		{"env is empty", map[string]interface{}{"runtime": map[string]interface{}{"env": []interface{}{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appEnvPairs(tc.spec); len(got) != 0 {
				t.Errorf("= %v, want empty", got)
			}
		})
	}
}
