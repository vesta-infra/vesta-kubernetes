package controllers

import (
	"testing"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Where an app's ingress points decides whether a request to a sleeping app fails or waits.
func TestIngressBackend(t *testing.T) {
	cases := []struct {
		name          string
		atRest        bool
		ready         int32
		wakeOnTraffic bool
		want          string
	}{
		{"running and ready", false, 2, true, "web"},
		{"asleep", true, 0, true, activatorName},

		// The window the activator exists for. Pointing back at the app the moment
		// somebody asks for it to run sends requests to a Service with no ready endpoints,
		// so the first requests after a wake fail -- which is exactly what this is meant
		// to prevent.
		{"woken but nothing serving yet", false, 0, true, activatorName},

		// Without wake-on-traffic there is nothing to stand in. Pointing at an activator
		// that is not running would turn a failing request into a differently failing one.
		{"asleep with wake-on-traffic off", true, 0, false, "web"},
		{"running with wake-on-traffic off", false, 1, false, "web"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ingressBackend("web", tc.atRest, tc.ready, tc.wakeOnTraffic); got != tc.want {
				t.Errorf("ingressBackend(atRest=%v, ready=%d, wake=%v) = %q, want %q",
					tc.atRest, tc.ready, tc.wakeOnTraffic, got, tc.want)
			}
		})
	}
}

// Both flags are needed, and the reason is an upgrade trap.
//
// Scale-to-zero did nothing for a long time, so any app whose Sleep button was ever pressed
// carries sleep.enabled=true and has been running normally ever since. If eligibility alone
// armed the sweeper or the ingress repoint, upgrading would change the behaviour of every
// one of those apps at once.
func TestSleepPolicyNeedsBothFlags(t *testing.T) {
	cases := []struct {
		name       string
		sleep      *vestav1alpha1.SleepConfig
		wantAuto   bool
		wantOnWake bool
	}{
		{"absent", nil, false, false},
		{"eligible only, as every upgraded app is", &vestav1alpha1.SleepConfig{Enabled: true}, false, false},
		{
			"eligible and opted in",
			&vestav1alpha1.SleepConfig{Enabled: true, AutoSleep: boolPtr(true), WakeOnTraffic: boolPtr(true)},
			true, true,
		},
		{
			"opted in but not eligible",
			&vestav1alpha1.SleepConfig{Enabled: false, AutoSleep: boolPtr(true), WakeOnTraffic: boolPtr(true)},
			false, false,
		},
		{
			"explicitly opted out",
			&vestav1alpha1.SleepConfig{Enabled: true, AutoSleep: boolPtr(false), WakeOnTraffic: boolPtr(false)},
			false, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sleep.AutoSleepEnabled(); got != tc.wantAuto {
				t.Errorf("AutoSleepEnabled() = %v, want %v", got, tc.wantAuto)
			}
			if got := tc.sleep.WakeOnTrafficEnabled(); got != tc.wantOnWake {
				t.Errorf("WakeOnTrafficEnabled() = %v, want %v", got, tc.wantOnWake)
			}
		})
	}
}

// An activator in a namespace where nothing uses it is a pod doing nothing, which is a poor
// look for a feature whose purpose is not running pods.
func TestActivatorOnlyWhereItIsUsed(t *testing.T) {
	app := func(project string, wake bool) vestav1alpha1.VestaApp {
		s := &vestav1alpha1.SleepConfig{Enabled: wake}
		if wake {
			s.WakeOnTraffic = boolPtr(true)
		}
		return vestav1alpha1.VestaApp{
			Spec: vestav1alpha1.VestaAppSpec{Project: project, Sleep: s},
		}
	}

	if activatorNeeded([]vestav1alpha1.VestaApp{app("acme", false)}, "acme-production", "acme") {
		t.Error("an activator was wanted for a project where nothing uses it")
	}
	if !activatorNeeded([]vestav1alpha1.VestaApp{app("acme", false), app("acme", true)}, "acme-production", "acme") {
		t.Error("an activator was not wanted despite an app opting into wake-on-traffic")
	}
	// Another project's apps must not pull one in here.
	if activatorNeeded([]vestav1alpha1.VestaApp{app("other", true)}, "acme-production", "acme") {
		t.Error("another project's app pulled in an activator")
	}
}

// The Service must select the pods the Deployment creates, or the ingress resolves to
// nothing and every request to a sleeping app times out instead of waiting.
func TestActivatorServiceMatchesItsDeployment(t *testing.T) {
	deploy := buildActivatorDeployment("acme-production", "vesta/activator:1", 1)
	svc := buildActivatorService("acme-production")

	for k, v := range svc.Spec.Selector {
		if deploy.Spec.Template.Labels[k] != v {
			t.Errorf("service selector %s=%s does not match the pod labels", k, v)
		}
	}
	if svc.Spec.Ports[0].TargetPort.IntVal != deploy.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort {
		t.Error("the service target port does not match the container port")
	}
	if deploy.Spec.Template.Spec.Containers[0].ReadinessProbe == nil {
		t.Error("no readiness probe; the ingress would route to an activator that is not up yet")
	}
}
