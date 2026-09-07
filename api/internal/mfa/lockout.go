package mfa

import "time"

// Lockout thresholds. A six-digit code with a one-step skew gives an attacker roughly a
// 3-in-a-million chance per guess, so capping attempts is what makes brute force
// hopeless. These counters live in Postgres rather than in memory: the API can run more
// than one replica, and an in-process map would multiply the real allowance by the
// replica count while also being lost on restart.
const (
	// LockoutWindow is how long failures accumulate before the count resets.
	LockoutWindow = 15 * time.Minute
	// LockoutThreshold is the number of failures inside a window that triggers a lock.
	LockoutThreshold = 5
	// LockoutDuration is the first lock. It escalates on repetition.
	LockoutDuration = 15 * time.Minute
	// ExtendedLockoutThreshold is the failure count that earns the longer lock.
	ExtendedLockoutThreshold = 10
	// ExtendedLockoutDuration is the escalated lock.
	ExtendedLockoutDuration = time.Hour
)

// LockoutState is one user's verification-failure record.
type LockoutState struct {
	FailedCount     int
	WindowStartedAt time.Time
	LockedUntil     *time.Time
}

// LockedFor reports how long a user must wait before trying again. Zero means not locked.
func LockedFor(s LockoutState, now time.Time) time.Duration {
	if s.LockedUntil == nil || !now.Before(*s.LockedUntil) {
		return 0
	}
	return s.LockedUntil.Sub(now)
}

// NextLockout returns the state to persist after a failed verification.
//
// Failures older than one window are forgotten, so someone who mistypes a code twice a
// day never accumulates their way into a lock.
func NextLockout(s LockoutState, now time.Time) LockoutState {
	next := LockoutState{
		FailedCount:     s.FailedCount + 1,
		WindowStartedAt: s.WindowStartedAt,
	}
	if s.WindowStartedAt.IsZero() || now.Sub(s.WindowStartedAt) > LockoutWindow {
		next.FailedCount = 1
		next.WindowStartedAt = now
	}

	switch {
	case next.FailedCount >= ExtendedLockoutThreshold:
		until := now.Add(ExtendedLockoutDuration)
		next.LockedUntil = &until
	case next.FailedCount >= LockoutThreshold:
		until := now.Add(LockoutDuration)
		next.LockedUntil = &until
	}
	return next
}

// ClearLockout is the state to persist after a successful verification.
func ClearLockout(now time.Time) LockoutState {
	return LockoutState{FailedCount: 0, WindowStartedAt: now}
}
