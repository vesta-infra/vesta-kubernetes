package mfa

import (
	"encoding/base32"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// rfcSecret is the RFC 6238 Appendix B seed "12345678901234567890", base32-encoded.
var rfcSecret = base32.StdEncoding.WithPadding(base32.NoPadding).
	EncodeToString([]byte("12345678901234567890"))

// The published SHA-1 vectors from RFC 6238 Appendix B. If this fails, the
// implementation is not TOTP and no authenticator app will ever agree with it.
func TestCodeAtMatchesRFC6238Vectors(t *testing.T) {
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}

	for _, c := range cases {
		t.Run(time.Unix(c.unix, 0).UTC().Format(time.RFC3339), func(t *testing.T) {
			got, err := CodeAt(rfcSecret, time.Unix(c.unix, 0).UTC())
			if err != nil {
				t.Fatalf("CodeAt: %v", err)
			}
			if got != c.want {
				t.Errorf("CodeAt(T=%d) = %s, want %s", c.unix, got, c.want)
			}
		})
	}
}

func TestVerifyTOTPAcceptsCurrentCode(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	code, err := CodeAt(rfcSecret, now)
	if err != nil {
		t.Fatalf("CodeAt: %v", err)
	}
	step, err := VerifyTOTP(rfcSecret, code, now, 0)
	if err != nil {
		t.Fatalf("VerifyTOTP: %v", err)
	}
	if step != Counter(now) {
		t.Errorf("matched step = %d, want %d", step, Counter(now))
	}
}

// One step either side covers ordinary clock drift between a phone and the server.
func TestVerifyTOTPAcceptsAdjacentSteps(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	for _, offset := range []time.Duration{-TOTPPeriod * time.Second, TOTPPeriod * time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			code, err := CodeAt(rfcSecret, now.Add(offset))
			if err != nil {
				t.Fatalf("CodeAt: %v", err)
			}
			if _, err := VerifyTOTP(rfcSecret, code, now, 0); err != nil {
				t.Errorf("code from %v offset rejected: %v", offset, err)
			}
		})
	}
}

func TestVerifyTOTPRejectsDistantSteps(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	for _, offset := range []time.Duration{
		-3 * TOTPPeriod * time.Second,
		-2 * TOTPPeriod * time.Second,
		2 * TOTPPeriod * time.Second,
		3 * TOTPPeriod * time.Second,
	} {
		t.Run(offset.String(), func(t *testing.T) {
			code, err := CodeAt(rfcSecret, now.Add(offset))
			if err != nil {
				t.Fatalf("CodeAt: %v", err)
			}
			if _, err := VerifyTOTP(rfcSecret, code, now, 0); !errors.Is(err, ErrInvalidCode) {
				t.Errorf("code from %v offset was accepted (err=%v)", offset, err)
			}
		})
	}
}

// A code stays valid for up to 90 seconds, so without a replay guard anyone who reads one
// over a shoulder or out of a log can reuse it. This is the regression that matters most
// in this file.
func TestVerifyTOTPRejectsReplayedCode(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	code, err := CodeAt(rfcSecret, now)
	if err != nil {
		t.Fatalf("CodeAt: %v", err)
	}

	step, err := VerifyTOTP(rfcSecret, code, now, 0)
	if err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}
	if _, err := VerifyTOTP(rfcSecret, code, now, step); !errors.Is(err, ErrCodeReplayed) {
		t.Errorf("replay error = %v, want ErrCodeReplayed", err)
	}
}

func TestVerifyTOTPRejectsStepsAtOrBelowLastCounter(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	current := Counter(now)
	code, err := CodeAt(rfcSecret, now)
	if err != nil {
		t.Fatalf("CodeAt: %v", err)
	}

	cases := []struct {
		name        string
		lastCounter uint64
		wantErr     error
	}{
		{"never used before", 0, nil},
		{"older step already used", current - 5, nil},
		{"this step already used", current, ErrCodeReplayed},
		{"a newer step already used", current + 5, ErrCodeReplayed},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := VerifyTOTP(rfcSecret, code, now, c.lastCounter)
			if c.wantErr == nil && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("error = %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestVerifyTOTPRejectsMalformedCodes(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	cases := []struct {
		name string
		code string
	}{
		{"empty", ""},
		{"too short", "12345"},
		{"too long", "1234567"},
		{"letters", "abcdef"},
		{"mixed", "12a456"},
		{"leading plus", "+12345"},
		{"whitespace inside", "123 56"},
		{"arabic-indic digits", "١٢٣٤٥٦"},
		{"negative", "-12345"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := VerifyTOTP(rfcSecret, c.code, now, 0); !errors.Is(err, ErrInvalidCode) {
				t.Errorf("VerifyTOTP(%q) error = %v, want ErrInvalidCode", c.code, err)
			}
		})
	}
}

// Users paste codes with stray spaces from password managers.
func TestVerifyTOTPTrimsSurroundingWhitespace(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	code, err := CodeAt(rfcSecret, now)
	if err != nil {
		t.Fatalf("CodeAt: %v", err)
	}
	if _, err := VerifyTOTP(rfcSecret, "  "+code+"\n", now, 0); err != nil {
		t.Errorf("padded code rejected: %v", err)
	}
}

func TestGenerateSecret(t *testing.T) {
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		s, err := GenerateSecret()
		if err != nil {
			t.Fatalf("GenerateSecret: %v", err)
		}
		if strings.Contains(s, "=") {
			t.Errorf("secret %q contains padding, which breaks manual entry in some apps", s)
		}
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
		if err != nil {
			t.Fatalf("secret is not valid unpadded base32: %v", err)
		}
		if len(raw) != totpSecretBytes {
			t.Errorf("secret decoded to %d bytes, want %d", len(raw), totpSecretBytes)
		}
		if seen[s] {
			t.Fatal("GenerateSecret returned a duplicate")
		}
		seen[s] = true
	}
}

// A generated secret has to actually work with the verifier.
func TestGenerateSecretRoundTripsThroughVerify(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	now := time.Now().UTC()
	code, err := CodeAt(secret, now)
	if err != nil {
		t.Fatalf("CodeAt: %v", err)
	}
	if _, err := VerifyTOTP(secret, code, now, 0); err != nil {
		t.Errorf("freshly generated secret failed verification: %v", err)
	}
}

func TestOTPAuthURL(t *testing.T) {
	u, err := url.Parse(OTPAuthURL("Vesta", "alice@example.com", rfcSecret))
	if err != nil {
		t.Fatalf("produced an unparseable URL: %v", err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Errorf("scheme/host = %s://%s, want otpauth://totp", u.Scheme, u.Host)
	}
	q := u.Query()
	for key, want := range map[string]string{
		"secret":    rfcSecret,
		"issuer":    "Vesta",
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(u.Path, "alice@example.com") {
		t.Errorf("label should carry the account, got %q", u.Path)
	}
}

// An issuer or account containing a slash, space or colon would otherwise corrupt the
// label and make the entry unreadable in the app.
func TestOTPAuthURLEscapesLabelComponents(t *testing.T) {
	raw := OTPAuthURL("Acme / Corp", "a b:c@example.com", rfcSecret)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("unparseable URL for awkward label: %v", err)
	}
	if strings.Count(u.EscapedPath(), "/") != 1 {
		t.Errorf("issuer slash was not escaped: %q", u.EscapedPath())
	}
	if u.Query().Get("issuer") != "Acme / Corp" {
		t.Errorf("issuer round trip = %q", u.Query().Get("issuer"))
	}
}

func TestOTPAuthURLDefaultsIssuer(t *testing.T) {
	u, _ := url.Parse(OTPAuthURL("", "alice", rfcSecret))
	if got := u.Query().Get("issuer"); got != "Vesta" {
		t.Errorf("default issuer = %q, want Vesta", got)
	}
}

func TestCounter(t *testing.T) {
	cases := []struct {
		unix int64
		want uint64
	}{
		{0, 0},
		{29, 0},
		{30, 1},
		{59, 1},
		{60, 2},
		{1111111111, 37037037},
	}
	for _, c := range cases {
		if got := Counter(time.Unix(c.unix, 0).UTC()); got != c.want {
			t.Errorf("Counter(%d) = %d, want %d", c.unix, got, c.want)
		}
	}
}

// Guards the underflow path in candidateSteps: near the Unix epoch, current-1 would wrap
// to a huge number.
func TestVerifyTOTPNearEpochDoesNotUnderflow(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	if _, err := VerifyTOTP(rfcSecret, "000000", now, 0); err == nil {
		t.Log("code happened to match; the point is that it did not panic")
	}
}
