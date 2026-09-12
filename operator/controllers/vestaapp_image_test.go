package controllers

import (
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func TestResolveImage(t *testing.T) {
	appImage := &vestav1alpha1.ImageConfig{Repository: "registry/app", Tag: "v1"}

	cases := []struct {
		name string
		app  *vestav1alpha1.ImageConfig
		env  *vestav1alpha1.ImageConfig
		want string
	}{
		{"no image config at all", nil, nil, "placeholder:latest"},
		{"app-level only", appImage, nil, "registry/app:v1"},
		{"app-level with no tag defaults to latest", &vestav1alpha1.ImageConfig{Repository: "registry/app"}, nil, "registry/app:latest"},
		{"env overrides tag only", appImage, &vestav1alpha1.ImageConfig{Tag: "v2"}, "registry/app:v2"},
		{"env overrides repository and tag", appImage, &vestav1alpha1.ImageConfig{Repository: "other/app", Tag: "v3"}, "other/app:v3"},
		{"env repository without tag defaults to latest", appImage, &vestav1alpha1.ImageConfig{Repository: "other/app"}, "other/app:latest"},
		{"empty env config falls back to app level", appImage, &vestav1alpha1.ImageConfig{}, "registry/app:v1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := &vestav1alpha1.VestaApp{Spec: vestav1alpha1.VestaAppSpec{Image: c.app}}
			if got := resolveImage(app, c.env); got != c.want {
				t.Errorf("resolveImage() = %q, want %q", got, c.want)
			}
		})
	}
}

// A deploy to one environment must not change what any other environment runs.
// Environments without their own tag stay pinned to the app-level default.
func TestResolveImageIsolatesEnvironments(t *testing.T) {
	app := &vestav1alpha1.VestaApp{
		Spec: vestav1alpha1.VestaAppSpec{
			Image: &vestav1alpha1.ImageConfig{Repository: "registry/app", Tag: "v1"},
			Environments: []vestav1alpha1.AppEnvironmentConfig{
				{Name: "staging", Image: &vestav1alpha1.ImageConfig{Tag: "staging-abc123"}},
				{Name: "production"},
			},
		},
	}

	want := map[string]string{
		"staging":    "registry/app:staging-abc123",
		"production": "registry/app:v1",
	}

	for _, env := range app.Spec.Environments {
		if got := resolveImage(app, env.Image); got != want[env.Name] {
			t.Errorf("env %s: resolveImage() = %q, want %q", env.Name, got, want[env.Name])
		}
	}
}
