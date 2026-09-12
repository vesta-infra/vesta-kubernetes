package mfa

import (
	"testing"
	"time"
)

var lockoutNow = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

func TestNextLockoutCountsFailures(t *testing.T) {
	state := LockoutState{}
	for i := 1; i < LockoutThreshold; i++ {
		state = NextLockout(state, lockoutNow)
		if state.FailedCount != i {
			t.Fatalf("after %d failures FailedCount = %d", i, state.FailedCount)
		}
		if state.LockedUntil != nil {
			t.Fatalf("locked after only %d failures, threshold is %d", i, LockoutThreshold)
		}
	}
}

func TestNextLockoutLocksAtThreshold(t *testing.T) {
	state := LockoutState{}
	for i := 0; i < LockoutThreshold; i++ {
		state = NextLockout(state, lockoutNow)
	}
	if state.LockedUntil == nil {
		t.Fatalf("not locked after %d failures", LockoutThreshold)
	}
	if got := state.LockedUntil.Sub(lockoutNow); got != LockoutDuration {
		t.Errorf("lock duration = %v, want %v", got, LockoutDuration)
	}
}

func TestNextLockoutEscalates(t *testing.T) {
	state := LockoutState{}
	for i := 0; i < ExtendedLockoutThreshold; i++ {
		state = NextLockout(state, lockoutNow)
	}
	if state.LockedUntil == nil {
		t.Fatal("not locked at the extended threshold")
	}
	if got := state.LockedUntil.Sub(lockoutNow); got != ExtendedLockoutDuration {
		t.Errorf("escalated duration = %v, want %v", got, ExtendedLockoutDuration)
	}
}

// Someone who mistypes a code twice a day must never accumulate their way into a lock.
func TestNextLockoutForgetsFailuresOlderThanTheWindow(t *testing.T) {
	state := LockoutState{}
	for i := 0; i < LockoutThreshold-1; i++ {
		state = NextLockout(state, lockoutNow)
	}

	later := lockoutNow.Add(LockoutWindow + time.Minute)
	state = NextLockout(state, later)

	if state.FailedCount != 1 {
		t.Errorf("FailedCount = %d after the window expired, want 1", state.FailedCount)
	}
	if state.LockedUntil != nil {
		t.Error("locked despite the earlier failures having aged out")
	}
	if !state.WindowStartedAt.Equal(later) {
		t.Error("window should restart at the new failure")
	}
}

func TestNextLockoutKeepsCountingInsideTheWindow(t *testing.T) {
	state := NextLockout(LockoutState{}, lockoutNow)
	state = NextLockout(state, lockoutNow.Add(LockoutWindow-time.Second))
	if state.FailedCount != 2 {
		t.Errorf("FailedCount = %d, want 2 for two failures inside one window", state.FailedCount)
	}
}

func TestLockedFor(t *testing.T) {
	until := lockoutNow.Add(10 * time.Minute)
	cases := []struct {
		name  string
		state LockoutState
		now   time.Time
		want  time.Duration
	}{
		{"never locked", LockoutState{}, lockoutNow, 0},
		{"locked with time remaining", LockoutState{LockedUntil: &until}, lockoutNow, 10 * time.Minute},
		{"lock just expired", LockoutState{LockedUntil: &until}, until, 0},
		{"lock long expired", LockoutState{LockedUntil: &until}, until.Add(time.Hour), 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LockedFor(c.state, c.now); got != c.want {
				t.Errorf("LockedFor = %v, want %v", got, c.want)
			}
		})
	}
}

func TestClearLockoutResetsEverything(t *testing.T) {
	state := LockoutState{}
	for i := 0; i < ExtendedLockoutThreshold; i++ {
		state = NextLockout(state, lockoutNow)
	}
	cleared := ClearLockout(lockoutNow)
	if cleared.FailedCount != 0 || cleared.LockedUntil != nil {
		t.Errorf("ClearLockout left %+v", cleared)
	}
	if LockedFor(cleared, lockoutNow) != 0 {
		t.Error("cleared state still reports as locked")
	}
}
