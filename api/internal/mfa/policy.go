package mfa

import "fmt"

// Policy describes who has to hold a second factor.
type Policy struct {
	// RequireAdmin makes 2FA mandatory for users with the admin role. Everyone else may
	// opt in. Off by default so the escape hatch is explicit in the deployment spec.
	RequireAdmin bool
}

// RequiredFor reports whether a user in this role must hold a second factor.
func RequiredFor(role string, p Policy) bool {
	return p.RequireAdmin && role == "admin"
}

// CanRemoveMethod reports whether a user may drop one factor, given what they would still
// hold afterwards.
//
// This is the anti-lockout rule. An admin under a mandatory policy who removes their last
// factor would be blocked from the entire API on their next login and, if they are the
// only admin, could not be reset by anyone. Backup codes do not count: they are a recovery
// path, not a factor someone can be expected to keep using.
func CanRemoveMethod(role string, remaining []Method, p Policy) error {
	if !RequiredFor(role, p) {
		return nil
	}
	for _, m := range remaining {
		if m == MethodTOTP || m == MethodWebAuthn {
			return nil
		}
	}
	return fmt.Errorf("two-factor authentication is required for the %s role: enrol another method before removing this one", role)
}
