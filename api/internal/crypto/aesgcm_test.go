package crypto

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	c, err := NewCipherFromBase64(key)
	if err != nil {
		t.Fatalf("NewCipherFromBase64: %v", err)
	}
	return c
}

func TestSealOpenRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	cases := []struct {
		name      string
		plaintext string
		aad       string
	}{
		{"typical totp secret", "JBSWY3DPEHPK3PXP", "user-123"},
		{"empty plaintext", "", "user-123"},
		{"empty aad", "secret", ""},
		{"long value", strings.Repeat("x", 4096), "user-123"},
		{"non-ascii", "sécret-🔐", "user-123"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sealed, err := c.Encrypt([]byte(tc.plaintext), tc.aad)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			got, err := c.Decrypt(sealed, tc.aad)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(got) != tc.plaintext {
				t.Errorf("round trip = %q, want %q", got, tc.plaintext)
			}
		})
	}
}

// The plaintext must never be recoverable from the stored form without the key.
func TestEncryptDoesNotLeakPlaintext(t *testing.T) {
	c := newTestCipher(t)
	secret := "JBSWY3DPEHPK3PXP"
	sealed, err := c.Encrypt([]byte(secret), "user-123")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(sealed, secret) {
		t.Error("ciphertext contains the plaintext")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	if err != nil {
		t.Fatalf("ciphertext is not base64 after the prefix: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Error("decoded ciphertext contains the plaintext")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	c := newTestCipher(t)
	sealed, err := c.Encrypt([]byte("secret"), "user-123")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	raw[len(raw)-1] ^= 0xff // flip a bit in the auth tag
	tampered := "v1:" + base64.StdEncoding.EncodeToString(raw)

	if _, err := c.Decrypt(tampered, "user-123"); !errors.Is(err, ErrBadCiphertext) {
		t.Errorf("tampered ciphertext error = %v, want ErrBadCiphertext", err)
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	a, b := newTestCipher(t), newTestCipher(t)
	sealed, err := a.Encrypt([]byte("secret"), "user-123")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := b.Decrypt(sealed, "user-123"); !errors.Is(err, ErrBadCiphertext) {
		t.Errorf("wrong-key error = %v, want ErrBadCiphertext", err)
	}
}

// Binding the user ID as additional data is what stops a ciphertext being lifted from one
// user's row into another's to clone their second factor.
func TestDecryptRejectsMismatchedAAD(t *testing.T) {
	c := newTestCipher(t)
	sealed, err := c.Encrypt([]byte("secret"), "user-123")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c.Decrypt(sealed, "user-456"); !errors.Is(err, ErrBadCiphertext) {
		t.Errorf("mismatched aad error = %v, want ErrBadCiphertext", err)
	}
}

// A repeated nonce under the same key breaks GCM badly, so this is worth asserting even
// though crypto/rand makes a collision implausible.
func TestEncryptProducesDistinctCiphertextsForSameInput(t *testing.T) {
	c := newTestCipher(t)
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		sealed, err := c.Encrypt([]byte("same"), "user-123")
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		if seen[sealed] {
			t.Fatal("identical ciphertext produced twice: nonce is not random")
		}
		seen[sealed] = true
	}
}

func TestDecryptRejectsMalformedInput(t *testing.T) {
	c := newTestCipher(t)
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"no version prefix", base64.StdEncoding.EncodeToString([]byte("whatever"))},
		{"unknown version prefix", "v2:" + base64.StdEncoding.EncodeToString([]byte("whatever"))},
		{"not base64", "v1:!!!not-base64!!!"},
		{"shorter than nonce", "v1:" + base64.StdEncoding.EncodeToString([]byte("short"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Decrypt(tc.input, "user-123"); err == nil {
				t.Errorf("Decrypt(%q) succeeded, want an error", tc.input)
			}
		})
	}
}

func TestNewCipherRejectsWrongKeySize(t *testing.T) {
	for _, n := range []int{0, 1, 16, 24, 31, 33, 64} {
		if _, err := NewCipher(make([]byte, n)); err == nil {
			t.Errorf("NewCipher accepted a %d-byte key, want only %d", n, KeyLen)
		}
	}
}

func TestNewCipherFromBase64(t *testing.T) {
	key := make([]byte, KeyLen)
	for i := range key {
		key[i] = byte(i)
	}

	// Keys get copied between shells, YAML and Secrets, so all four alphabets must work.
	for name, encoded := range map[string]string{
		"std":                    base64.StdEncoding.EncodeToString(key),
		"raw std":                base64.RawStdEncoding.EncodeToString(key),
		"url":                    base64.URLEncoding.EncodeToString(key),
		"raw url":                base64.RawURLEncoding.EncodeToString(key),
		"padded with whitespace": "  " + base64.StdEncoding.EncodeToString(key) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCipherFromBase64(encoded); err != nil {
				t.Errorf("NewCipherFromBase64(%q) = %v", encoded, err)
			}
		})
	}
}

func TestNewCipherFromBase64EmptyIsErrNoKey(t *testing.T) {
	for _, s := range []string{"", "   ", "\n"} {
		if _, err := NewCipherFromBase64(s); !errors.Is(err, ErrNoKey) {
			t.Errorf("NewCipherFromBase64(%q) = %v, want ErrNoKey", s, err)
		}
	}
}

// A nil Cipher is what callers hold when no key is configured; it must report ErrNoKey
// rather than panic, so TOTP degrades while WebAuthn keeps working.
func TestNilCipherReportsErrNoKey(t *testing.T) {
	var c *Cipher
	if _, err := c.Encrypt([]byte("x"), ""); !errors.Is(err, ErrNoKey) {
		t.Errorf("Encrypt on nil cipher = %v, want ErrNoKey", err)
	}
	if _, err := c.Decrypt("v1:abc", ""); !errors.Is(err, ErrNoKey) {
		t.Errorf("Decrypt on nil cipher = %v, want ErrNoKey", err)
	}
}

func TestGenerateKeyIsCorrectSizeAndUnique(t *testing.T) {
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		k, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			t.Fatalf("GenerateKey produced invalid base64: %v", err)
		}
		if len(raw) != KeyLen {
			t.Fatalf("key length = %d, want %d", len(raw), KeyLen)
		}
		if seen[k] {
			t.Fatal("GenerateKey returned a duplicate")
		}
		seen[k] = true
	}
}
