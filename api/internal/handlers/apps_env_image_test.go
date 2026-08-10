package handlers

import "testing"

// Editing an app rewrites spec.environments wholesale. The tag a deploy pinned to an
// environment lives there, so a patch that says nothing about images must not drop it —
// doing so rolls the environment back to the older app-level spec.image.tag.
func TestRestoreEnvImagesKeepsDeployedTags(t *testing.T) {
	existing := collectEnvImages([]interface{}{
		map[string]interface{}{
			"name":  "staging",
			"image": map[string]interface{}{"tag": "staging-abc123"},
		},
		map[string]interface{}{
			"name":  "production",
			"image": map[string]interface{}{"repository": "other/app", "tag": "v9"},
		},
		map[string]interface{}{"name": "preview"},
	})

	patchEnvs := []interface{}{
		map[string]interface{}{"name": "staging", "replicas": int64(2)},
		map[string]interface{}{"name": "production", "image": map[string]interface{}{"tag": "v10"}},
		map[string]interface{}{"name": "preview", "replicas": int64(1)},
	}

	result := restoreEnvImages(patchEnvs, existing)

	cases := []struct {
		env  string
		want map[string]interface{}
	}{
		{"staging", map[string]interface{}{"tag": "staging-abc123"}},
		{"production", map[string]interface{}{"tag": "v10"}},
		{"preview", nil},
	}

	for i, c := range cases {
		envMap := result[i].(map[string]interface{})
		img, present := envMap["image"]
		if c.want == nil {
			if present {
				t.Errorf("env %s: expected no image override, got %v", c.env, img)
			}
			continue
		}
		imgMap, ok := img.(map[string]interface{})
		if !ok {
			t.Fatalf("env %s: expected image map, got %v", c.env, img)
		}
		for k, want := range c.want {
			if imgMap[k] != want {
				t.Errorf("env %s: image[%q] = %v, want %v", c.env, k, imgMap[k], want)
			}
		}
	}
}

// An explicit null clears the override so an environment can fall back to spec.image.
func TestRestoreEnvImagesClearsOnExplicitNull(t *testing.T) {
	existing := collectEnvImages([]interface{}{
		map[string]interface{}{
			"name":  "staging",
			"image": map[string]interface{}{"tag": "staging-abc123"},
		},
	})

	result := restoreEnvImages([]interface{}{
		map[string]interface{}{"name": "staging", "image": nil},
	}, existing)

	envMap := result[0].(map[string]interface{})
	if img, present := envMap["image"]; present {
		t.Errorf("expected image override to be cleared, got %v", img)
	}
}
