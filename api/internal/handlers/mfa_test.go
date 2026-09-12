package handlers

import (
	"os"
	"strings"
	"testing"
	"time"

	"kubernetes.getvesta.sh/api/internal/mfa"
	"kubernetes.getvesta.sh/api/internal/middleware"
)

func TestAllowedWebAuthnOriginsParsesList(t *testing.T) {
	t.Setenv("VESTA_ALLOWED_ORIGINS", " https://vesta.example.com , https://ui.example.com ")
	got := allowedWebAuthnOrigins()
	if len(got) != 2 || got[0] != "https://vesta.example.com" || got[1] != "https://ui.example.com" {
		t.Fatalf("origins = %v", got)
	}
}

// An empty allowlist must mean "unset", not "allow nothing" -- ResolveRelyingParty treats
// nil as the zero-configuration path, and an empty non-nil slice would too, but a slice
// of empty strings would not.
func TestAllowedWebAuthnOriginsUnsetIsNil(t *testing.T) {
	t.Setenv("VESTA_ALLOWED_ORIGINS", "")
	if got := allowedWebAuthnOrigins(); got != nil {
		t.Fatalf("origins = %v, want nil", got)
	}
}

func TestAllowedWebAuthnOriginsSkipsEmptyEntries(t *testing.T) {
	t.Setenv("VESTA_ALLOWED_ORIGINS", "https://a.example.com,,  ,https://b.example.com")
	got := allowedWebAuthnOrigins()
	if len(got) != 2 {
		t.Fatalf("origins = %v, want 2 entries", got)
	}
}

// The QR is rendered from the same URL the manual-entry field shows, so a mismatch here
// would mean the two enrollment paths disagree about the secret.
func TestQRDataURIMatchesOTPAuthURL(t *testing.T) {
	secret, err := mfa.GenerateSecret()
	if err != nil {
		t.Fatalf("generating secret: %v", err)
	}

	uri, err := mfa.QRDataURI(mfa.OTPAuthURL("Vesta", "someone@example.com", secret))
	if err != nil {
		t.Fatalf("rendering QR: %v", err)
	}
	if !strings.HasPrefix(uri, "data:image/png;base64,") {
		t.Fatalf("QR is not a PNG data URI: %.40s", uri)
	}
	if len(uri) < 500 {
		t.Errorf("QR data URI is suspiciously short (%d bytes), the image may be blank", len(uri))
	}
}

func TestQRDataURIRejectsNonOTPAuthURL(t *testing.T) {
	if _, err := mfa.QRDataURI("https://example.com/not-an-otpauth-url"); err == nil {
		t.Fatal("expected a non-otpauth URL to be rejected")
	}
}

// The grant travels in a header, not a query parameter: query strings land in access logs
// and proxy traces, and a grant there could be replayed from them within its lifetime.
func TestReauthHeaderIsNotAQueryParameter(t *testing.T) {
	if !strings.HasPrefix(ReauthHeader, "X-") {
		t.Errorf("ReauthHeader = %q, expected a custom request header", ReauthHeader)
	}
	if strings.ContainsAny(ReauthHeader, "?&= ") {
		t.Errorf("ReauthHeader = %q looks like a query parameter", ReauthHeader)
	}
}

// A grant has to outlive reading a dialog and reaching for a security key, without
// lingering long enough that one left in a closed tab is still spendable.
func TestReauthTTLIsShortButUsable(t *testing.T) {
	if reauthTTL < time.Minute {
		t.Errorf("reauthTTL = %s, too short to find a security key", reauthTTL)
	}
	if reauthTTL > 15*time.Minute {
		t.Errorf("reauthTTL = %s, a forgotten confirmation stays spendable too long", reauthTTL)
	}
}

// Removal is gated on a fresh proof of identity. If any of these stops requiring one, a
// stolen session can strip the account's second factor or mint recovery codes that
// outlive the victim's password change.
func TestDestructiveMFAHandlersRequireReauth(t *testing.T) {
	source, err := os.ReadFile("mfa.go")
	if err != nil {
		t.Fatalf("reading mfa.go: %v", err)
	}

	for _, fn := range []string{"DisableTOTP", "DeleteWebAuthnCredential", "RegenerateBackupCodes"} {
		body, ok := functionBody(string(source), "func (h *Handler) "+fn+"(")
		if !ok {
			t.Errorf("%s not found in mfa.go", fn)
			continue
		}
		if !strings.Contains(body, "h.requireReauth(") {
			t.Errorf("%s does not require re-authentication", fn)
		}
	}
}

// functionBody returns the text between a function's opening line and the next
// top-level closing brace.
func functionBody(source, signature string) (string, bool) {
	start := strings.Index(source, signature)
	if start == -1 {
		return "", false
	}
	rest := source[start:]
	if end := strings.Index(rest, "\n}\n"); end != -1 {
		return rest[:end], true
	}
	return rest, true
}

// Adding a factor to an account that already holds one is gated, but the first one is
// not. Calling the unconditional requireReauth here would deadlock enrollment: a user
// with no factor cannot produce a passkey assertion, and under a mandatory policy they
// are holding an enroll token that cannot reach the re-auth endpoints at all.
func TestEnrollmentGatesOnlyWhenAFactorExists(t *testing.T) {
	source, err := os.ReadFile("mfa.go")
	if err != nil {
		t.Fatalf("reading mfa.go: %v", err)
	}

	for _, fn := range []string{"EnrollTOTP", "BeginWebAuthnRegistration"} {
		body, ok := functionBody(string(source), "func (h *Handler) "+fn+"(")
		if !ok {
			t.Errorf("%s not found in mfa.go", fn)
			continue
		}
		if !strings.Contains(body, "h.requireReauthForNewFactor(") {
			t.Errorf("%s does not gate a second factor on re-authentication", fn)
		}
		if strings.Contains(body, "h.requireReauth(") {
			t.Errorf("%s uses the unconditional gate, which would block first-time enrollment", fn)
		}
	}
}

// A half-authenticated token must not be able to mint a grant. That is only safe because
// such a user has no factor by construction -- issuePartialTokenIfNeeded hands out an
// enroll token only when ListEnrollments came back empty -- and so is never asked for one.
func TestReauthIsUnreachableWithAPartialToken(t *testing.T) {
	for _, spec := range middleware.PartialAuthRoutes() {
		if strings.Contains(spec.Pattern, "/auth/reauth/") {
			t.Errorf("%s %s is reachable mid-authentication; a partial token could mint a grant",
				spec.Method, spec.Pattern)
		}
		if spec.Method == "DELETE" {
			t.Errorf("%s %s: a partial token can reach a destructive route", spec.Method, spec.Pattern)
		}
	}
}

// The exemption is driven by the enrollment list, so an unreadable list must fail closed.
// Treating a database error as "no factors" would be a way straight past the gate.
func TestRequireReauthForNewFactorFailsClosed(t *testing.T) {
	source, err := os.ReadFile("reauth.go")
	if err != nil {
		t.Fatalf("reading reauth.go: %v", err)
	}
	body, ok := functionBody(string(source), "func (h *Handler) requireReauthForNewFactor(")
	if !ok {
		t.Fatal("requireReauthForNewFactor not found")
	}

	errIdx := strings.Index(body, "if err != nil")
	emptyIdx := strings.Index(body, "len(enrollments) == 0")
	if errIdx == -1 || emptyIdx == -1 {
		t.Fatal("expected both an error check and an empty-list check")
	}
	if errIdx > emptyIdx {
		t.Error("the error check must come before the empty-list exemption, or a failed read is treated as 'no factors'")
	}
}
