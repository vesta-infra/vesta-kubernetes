// Package crypto provides authenticated encryption for values Vesta must be able to read
// back, as opposed to verify. Passwords are bcrypt-hashed and API tokens are SHA-256
// hashed; a TOTP shared secret is different, because generating a code requires the
// original bytes. This is the only reversible-secret path in the codebase.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeyLen is the required key size: AES-256.
const KeyLen = 32

// versionPrefix tags every ciphertext so the scheme can change later without guessing at
// what old rows contain. Costs six bytes and makes key rotation a migration rather than a
// rewrite.
const versionPrefix = "v1:"

var (
	// ErrNoKey means encryption was attempted with no key configured. Callers should
	// treat this as "this feature is unavailable", not as a fatal error - WebAuthn needs
	// no key and must keep working.
	ErrNoKey = errors.New("crypto: no encryption key configured")

	// ErrBadCiphertext covers anything that fails to decrypt: truncation, a wrong key, or
	// tampering. It is deliberately indistinguishable between those cases.
	ErrBadCiphertext = errors.New("crypto: ciphertext is invalid or was encrypted with a different key")
)

// Cipher encrypts and decrypts small secrets with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", KeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// NewCipherFromBase64 builds a Cipher from a standard or URL-encoded base64 key, which is
// how the key travels through env vars and Kubernetes Secrets.
func NewCipherFromBase64(encoded string) (*Cipher, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, ErrNoKey
	}
	key, err := decodeBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: key is not valid base64: %w", err)
	}
	return NewCipher(key)
}

// GenerateKey returns a fresh base64-encoded AES-256 key.
func GenerateKey() (string, error) {
	key := make([]byte, KeyLen)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("crypto: generating key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// Encrypt seals plaintext and returns "v1:" followed by base64(nonce||ciphertext||tag).
//
// aad is bound into the authentication tag but not stored. Pass the user ID: it means a
// ciphertext lifted from one row cannot be pasted into another and still decrypt.
func (c *Cipher) Encrypt(plaintext []byte, aad string) (string, error) {
	if c == nil || c.aead == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: generating nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, []byte(aad))
	return versionPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. aad must match what was passed to Encrypt.
func (c *Cipher) Decrypt(encoded, aad string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrNoKey
	}
	if !strings.HasPrefix(encoded, versionPrefix) {
		return nil, fmt.Errorf("%w: missing %q version prefix", ErrBadCiphertext, versionPrefix)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, versionPrefix))
	if err != nil {
		return nil, ErrBadCiphertext
	}
	if len(raw) < c.aead.NonceSize() {
		return nil, ErrBadCiphertext
	}

	nonce, ciphertext := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		// Deliberately opaque: a wrong key and a tampered tag look identical.
		return nil, ErrBadCiphertext
	}
	return plaintext, nil
}

// decodeBase64 accepts both the standard and URL alphabets, with or without padding,
// because keys get copied between shells, YAML, and Kubernetes Secrets.
func decodeBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}
