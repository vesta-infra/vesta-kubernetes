package mfa

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP parameters. These are the values every authenticator app assumes when a QR code
// omits them, so deviating buys nothing and breaks manual entry.
const (
	TOTPDigits = 6
	TOTPPeriod = 30 // seconds
	// TOTPSkew is how many steps either side of now are accepted, so a code is valid for
	// at most 90 seconds. Widening this multiplies the brute-force surface linearly and
	// is not worth it.
	TOTPSkew = 1
	// totpSecretBytes is 160 bits, the size RFC 4226 recommends for HMAC-SHA1.
	totpSecretBytes = 20
)

// GenerateSecret returns a new base32-encoded TOTP secret, unpadded because padding
// characters break some authenticator apps' manual entry.
func GenerateSecret() (string, error) {
	buf := make([]byte, totpSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mfa: generating TOTP secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// OTPAuthURL builds the otpauth:// URI an authenticator scans.
//
// Both issuer and account are escaped, and issuer is repeated as a query parameter as
// well as in the label - that duplication is what the spec requires, and apps that read
// only one of the two show "Unknown" without it.
func OTPAuthURL(issuer, account, secret string) string {
	if issuer == "" {
		issuer = "Vesta"
	}
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprintf("%d", TOTPDigits))
	v.Set("period", fmt.Sprintf("%d", TOTPPeriod))

	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	return "otpauth://totp/" + label + "?" + v.Encode()
}

// Counter converts a wall-clock time to a TOTP step number.
func Counter(t time.Time) uint64 {
	return uint64(t.UTC().Unix() / TOTPPeriod)
}

// CodeAt returns the code a correctly-configured authenticator shows at time t. Exported
// so tests can drive it against the RFC 6238 vectors.
func CodeAt(secret string, t time.Time) (string, error) {
	return totp.GenerateCodeCustom(secret, t, totp.ValidateOpts{
		Period:    TOTPPeriod,
		Skew:      0,
		Digits:    TOTPDigits,
		Algorithm: otp.AlgorithmSHA1,
	})
}

// VerifyTOTP checks a submitted code and returns the step it matched.
//
// The step is the point: the caller persists it and passes it back as lastCounter, so a
// code cannot be replayed inside the window it stays valid for. Without that, anyone who
// reads a code over someone's shoulder, out of a proxy log, or from a phished form has up
// to 90 seconds to reuse it. pquerna/otp's own Validate cannot support this because it
// only reports a boolean.
//
// A zero lastCounter means "no code accepted yet" and imposes no lower bound.
func VerifyTOTP(secret, code string, now time.Time, lastCounter uint64) (uint64, error) {
	code = strings.TrimSpace(code)
	if !isAllDigits(code) || len(code) != TOTPDigits {
		return 0, ErrInvalidCode
	}

	current := Counter(now)
	// Walk newest first: the overwhelmingly common case is the current step.
	for offset := 0; offset <= TOTPSkew; offset++ {
		for _, step := range candidateSteps(current, offset) {
			expected, err := CodeAt(secret, time.Unix(int64(step)*TOTPPeriod, 0).UTC())
			if err != nil {
				return 0, fmt.Errorf("mfa: generating comparison code: %w", err)
			}
			if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
				continue
			}
			if lastCounter != 0 && step <= lastCounter {
				return 0, ErrCodeReplayed
			}
			return step, nil
		}
	}
	return 0, ErrInvalidCode
}

// candidateSteps returns the steps to try at a given distance from now: the current step
// first, then the one after and the one before.
func candidateSteps(current uint64, offset int) []uint64 {
	if offset == 0 {
		return []uint64{current}
	}
	steps := make([]uint64, 0, 2)
	steps = append(steps, current+uint64(offset))
	if current >= uint64(offset) {
		steps = append(steps, current-uint64(offset))
	}
	return steps
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		// Explicitly ASCII: unicode.IsDigit accepts other scripts' digits, which would
		// not match anything an authenticator produces.
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
