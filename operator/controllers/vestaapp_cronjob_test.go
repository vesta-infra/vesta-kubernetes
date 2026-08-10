package controllers

import (
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func boolPtr(b bool) *bool { return &b }

func TestIsCronjobEnabled(t *testing.T) {
	cases := []struct {
		name string
		cj   vestav1alpha1.CronjobConfig
		env  string
		want bool
	}{
		{
			name: "no flags defaults to enabled",
			cj:   vestav1alpha1.CronjobConfig{Name: "cleanup"},
			env:  "production",
			want: true,
		},
		{
			name: "cronjob-level disable applies everywhere",
			cj:   vestav1alpha1.CronjobConfig{Name: "cleanup", Enabled: boolPtr(false)},
			env:  "production",
			want: false,
		},
		{
			name: "environment override disables a single environment",
			cj: vestav1alpha1.CronjobConfig{Name: "cleanup", Environments: []vestav1alpha1.CronjobEnvironmentOverride{
				{Name: "staging", Enabled: boolPtr(false)},
			}},
			env:  "staging",
			want: false,
		},
		{
			name: "environment override leaves other environments alone",
			cj: vestav1alpha1.CronjobConfig{Name: "cleanup", Environments: []vestav1alpha1.CronjobEnvironmentOverride{
				{Name: "staging", Enabled: boolPtr(false)},
			}},
			env:  "production",
			want: true,
		},
		{
			name: "environment override can re-enable a globally disabled cronjob",
			cj: vestav1alpha1.CronjobConfig{Name: "cleanup", Enabled: boolPtr(false), Environments: []vestav1alpha1.CronjobEnvironmentOverride{
				{Name: "production", Enabled: boolPtr(true)},
			}},
			env:  "production",
			want: true,
		},
		{
			name: "environment entry without an enabled flag falls back to cronjob level",
			cj: vestav1alpha1.CronjobConfig{Name: "cleanup", Enabled: boolPtr(false), Environments: []vestav1alpha1.CronjobEnvironmentOverride{
				{Name: "production", Schedule: "0 4 * * *"},
			}},
			env:  "production",
			want: false,
		},
	}

	r := &VestaAppReconciler{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.isCronjobEnabled(c.cj, c.env); got != c.want {
				t.Errorf("isCronjobEnabled(%s) = %v, want %v", c.env, got, c.want)
			}
		})
	}
}
