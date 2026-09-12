// Package mfa holds the second-factor logic that does not touch the database, the clock,
// or HTTP. Vesta's test suite has no fixtures, no mocks and no test database - handlers
// are never exercised through a router - so anything that must be verifiable has to be a
// pure function. That is the organising constraint of this package.
package mfa

import "time"

// Method identifies a way of satisfying a second-factor challenge.
type Method string

const (
	MethodTOTP       Method = "totp"
	MethodWebAuthn   Method = "webauthn"
	MethodBackupCode Method = "backup"
)

// Enrollment is one registered factor belonging to a user.
//
// The set of enrollments is derived by querying the TOTP and WebAuthn tables rather than
// stored as a flag, so there is nothing to drift out of sync. This single list drives the
// login branch, the status endpoint, the client's method picker and the last-factor
// guard, which is what makes "enrol either method" work.
type Enrollment struct {
	Method     Method     `json:"method"`
	ID         string     `json:"id,omitempty"` // credential UUID; empty for TOTP
	Name       string     `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// Methods reduces enrollments to the distinct methods present, preserving order.
func Methods(enrollments []Enrollment) []Method {
	out := make([]Method, 0, 2)
	for _, e := range enrollments {
		if !containsMethod(out, e.Method) {
			out = append(out, e.Method)
		}
	}
	return out
}

func containsMethod(haystack []Method, needle Method) bool {
	for _, m := range haystack {
		if m == needle {
			return true
		}
	}
	return false
}
