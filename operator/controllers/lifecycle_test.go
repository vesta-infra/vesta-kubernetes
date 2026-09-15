package controllers

import "testing"

func i32(v int32) *int32 { return &v }

// The production failure this guards: sleep never worked. The API expressed "sleep" by
// patching status.phase, which a status subresource discards, and the operator zeroed
// replicas only once the phase already read "Sleeping" -- a phase that was itself derived
// from replicas already being zero. Reading the instruction from the spec is what breaks
// the cycle, so every resting state must reach zero without consulting status at all.
func TestResolveReplicas(t *testing.T) {
	cases := []struct {
		name         string
		desiredState string
		envReplicas  *int32
		autoscale    bool
		wantReplicas int32
		wantWrite    bool
	}{
		{"empty state means running", "", nil, false, 1, true},
		{"empty state honours the env replica count", "", i32(4), false, 4, true},
		{"running is the same as empty", "running", i32(4), false, 4, true},

		{"sleeping is zero", "sleeping", i32(4), false, 0, true},
		{"stopped is zero", "stopped", i32(4), false, 0, true},
		{"sleeping is zero even with no env replicas", "sleeping", nil, false, 0, true},

		// The autoscaler owns spec.replicas while the app is running, so the operator
		// must not write it -- otherwise every reconcile drags the app back to its floor.
		{"autoscaling running does not write replicas", "running", i32(4), true, 4, false},
		{"autoscaling empty state does not write replicas", "", nil, true, 1, false},

		// ...but zero has to win over the autoscaler, or the HPA keeps the app alive and
		// the instruction to sleep is quietly ignored.
		{"autoscaling sleeping still writes zero", "sleeping", i32(4), true, 0, true},
		{"autoscaling stopped still writes zero", "stopped", i32(4), true, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replicas, write := resolveReplicas(tc.desiredState, tc.envReplicas, tc.autoscale)
			if replicas != tc.wantReplicas {
				t.Errorf("replicas = %d, want %d", replicas, tc.wantReplicas)
			}
			if write != tc.wantWrite {
				t.Errorf("write = %v, want %v", write, tc.wantWrite)
			}
		})
	}
}

// A stopped app used to revert within one 30-second resync: nothing set the phase from the
// spec, so the next status pass diagnosed zero replicas as "Pending" and the reconcile
// after that restored them. The phase has to come from the instruction, not the symptom.
func TestRestingPhase(t *testing.T) {
	cases := []struct {
		desiredState string
		wantPhase    string
		wantResting  bool
	}{
		{"", "", false},
		{"running", "", false},
		{"sleeping", "Sleeping", true},
		{"stopped", "Stopped", true},
	}

	for _, tc := range cases {
		t.Run("desiredState="+tc.desiredState, func(t *testing.T) {
			phase, resting := restingPhase(tc.desiredState)
			if phase != tc.wantPhase || resting != tc.wantResting {
				t.Errorf("restingPhase(%q) = (%q, %v), want (%q, %v)",
					tc.desiredState, phase, resting, tc.wantPhase, tc.wantResting)
			}
		})
	}
}

// Every phase restingPhase can return must be a legal value of the CRD enum, or the
// operator writes a status the API server rejects and the app is stuck reporting whatever
// it last managed to persist.
func TestRestingPhasesAreValidEnumValues(t *testing.T) {
	valid := map[string]bool{
		"Pending": true, "Building": true, "Deploying": true, "Running": true,
		"Degraded": true, "Failed": true, "Sleeping": true, "Stopped": true,
		"CrashLoopBackOff": true,
	}

	for _, state := range []string{"sleeping", "stopped"} {
		phase, ok := restingPhase(state)
		if !ok {
			t.Fatalf("restingPhase(%q) reported no resting phase", state)
		}
		if !valid[phase] {
			t.Errorf("restingPhase(%q) = %q, which is not in the VestaAppStatus.Phase enum", state, phase)
		}
	}
}

func TestNormalizeDesiredState(t *testing.T) {
	if got := normalizeDesiredState(""); got != "running" {
		t.Errorf("normalizeDesiredState(\"\") = %q, want \"running\" -- apps written before "+
			"spec.desiredState existed must keep running", got)
	}
	for _, s := range []string{"running", "sleeping", "stopped"} {
		if got := normalizeDesiredState(s); got != s {
			t.Errorf("normalizeDesiredState(%q) = %q, want unchanged", s, got)
		}
	}
}

// Setting an environment's replicas to zero reported Pending.
//
// It is a deliberate act with the same outcome as sleeping, but it leaves desiredState
// alone, so restingPhase said nothing and every "totalDesired > 0" case in the phase switch
// failed. The app fell through to the default — Pending, which reads as "coming up shortly"
// for something that is never coming up, and shows no reason because there is nothing wrong.
func TestScalingToZeroReportsSleepingNotPending(t *testing.T) {
	phase, ok := zeroReplicaPhase(1, 0)
	if !ok {
		t.Fatal("an app whose Deployment asks for zero replicas reported no resting phase")
	}
	if phase != "Sleeping" {
		t.Errorf("phase = %q, want Sleeping", phase)
	}
}

// The two zeroes have to stay apart. Nothing deployed yet is genuinely Pending; saying
// Sleeping there would report a brand new app as deliberately asleep before it had ever
// been asked to run.
func TestNothingDeployedYetIsStillPending(t *testing.T) {
	if _, ok := zeroReplicaPhase(0, 0); ok {
		t.Error("an app with no Deployment at all was called Sleeping; it has not been " +
			"created yet, which is what Pending means")
	}
}

// A running app must not be swept up by this.
func TestRunningAppsAreUnaffected(t *testing.T) {
	cases := []struct {
		name     string
		observed int
		desired  int32
	}{
		{"one replica", 1, 1},
		{"several replicas", 2, 6},
		{"one environment up, one scaled down", 2, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := zeroReplicaPhase(tc.observed, tc.desired); ok {
				t.Error("an app with replicas was reported as resting")
			}
		})
	}
}

// Sleep and stop still win. They are read from the spec and say why there are no pods,
// which a replica count cannot: "asleep until traffic arrives" and "scaled to zero" are
// different states an operator should be able to tell apart.
func TestExplicitRestingStatesTakePrecedence(t *testing.T) {
	for _, state := range []string{"sleeping", "stopped"} {
		phase, ok := restingPhase(state)
		if !ok {
			t.Fatalf("%s reported no resting phase", state)
		}
		if state == "stopped" && phase != "Stopped" {
			t.Errorf("a stopped app reported %q; scaling to zero must not make it "+
				"indistinguishable from a sleeping one", phase)
		}
	}
}
