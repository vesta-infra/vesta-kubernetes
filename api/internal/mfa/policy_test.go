package mfa

import (
	"testing"
	"time"
)

func TestRequiredFor(t *testing.T) {
	cases := []struct {
		name   string
		role   string
		policy Policy
		want   bool
	}{
		{"admin under a mandatory policy", "admin", Policy{RequireAdmin: true}, true},
		{"admin with enforcement off", "admin", Policy{RequireAdmin: false}, false},
		{"developer under a mandatory policy", "developer", Policy{RequireAdmin: true}, false},
		{"viewer under a mandatory policy", "viewer", Policy{RequireAdmin: true}, false},
		{"unknown role", "robot", Policy{RequireAdmin: true}, false},
		{"empty role", "", Policy{RequireAdmin: true}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RequiredFor(c.role, c.policy); got != c.want {
				t.Errorf("RequiredFor(%q, %+v) = %v, want %v", c.role, c.policy, got, c.want)
			}
		})
	}
}

// The anti-lockout rule. An admin who removes their last factor under a mandatory policy
// would be blocked from the whole API at next login, and if they are the only admin
// nobody could reset them.
func TestCanRemoveMethod(t *testing.T) {
	mandatory := Policy{RequireAdmin: true}
	cases := []struct {
		name      string
		role      string
		remaining []Method
		policy    Policy
		wantErr   bool
	}{
		{"admin keeps a passkey", "admin", []Method{MethodWebAuthn}, mandatory, false},
		{"admin keeps totp", "admin", []Method{MethodTOTP}, mandatory, false},
		{"admin keeps both", "admin", []Method{MethodTOTP, MethodWebAuthn}, mandatory, false},
		{"admin left with nothing", "admin", nil, mandatory, true},
		{"admin left with only backup codes", "admin", []Method{MethodBackupCode}, mandatory, true},
		{"developer may remove everything", "developer", nil, mandatory, false},
		{"viewer may remove everything", "viewer", nil, mandatory, false},
		{"admin may remove everything when enforcement is off", "admin", nil, Policy{}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CanRemoveMethod(c.role, c.remaining, c.policy)
			if (err != nil) != c.wantErr {
				t.Errorf("CanRemoveMethod(%q, %v) error = %v, wantErr %v", c.role, c.remaining, err, c.wantErr)
			}
		})
	}
}

// Backup codes are a recovery path, not a factor someone can keep using, so they must not
// satisfy the requirement on their own.
func TestBackupCodesAloneDoNotSatisfyTheRequirement(t *testing.T) {
	err := CanRemoveMethod("admin", []Method{MethodBackupCode}, Policy{RequireAdmin: true})
	if err == nil {
		t.Error("an admin was allowed to drop to backup codes only")
	}
}

func TestMethods(t *testing.T) {
	now := time.Now()
	enrollments := []Enrollment{
		{Method: MethodWebAuthn, ID: "a", CreatedAt: now},
		{Method: MethodWebAuthn, ID: "b", CreatedAt: now},
		{Method: MethodTOTP, CreatedAt: now},
	}
	got := Methods(enrollments)
	if len(got) != 2 {
		t.Fatalf("Methods = %v, want two distinct methods", got)
	}
	if got[0] != MethodWebAuthn || got[1] != MethodTOTP {
		t.Errorf("Methods = %v, want order preserved as [webauthn totp]", got)
	}
	if len(Methods(nil)) != 0 {
		t.Error("Methods(nil) should be empty")
	}
}
