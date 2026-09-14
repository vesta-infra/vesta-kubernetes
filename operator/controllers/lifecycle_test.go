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
