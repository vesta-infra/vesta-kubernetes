package bundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PublicKeyPrefix tags a key so an operator pasting one into the wrong field gets an
// error instead of a decode failure fifty lines later, and so the scheme can change
// without having to guess what an untagged key was.
const PublicKeyPrefix = "vesta1:pub:"

// aad and the info string are constants bound into every bundle. Including both public
// keys in info is what stops a bundle sealed for instance B being replayed at instance C:
// C derives a different key and the GCM tag fails.
const (
	aad      = "vesta-bundle-v1"
	infoBase = "vesta-bundle-v1"
)

var (
	// ErrWrongRecipient means the bundle was sealed for a different instance. It is
	// reported separately from ErrBadBundle because the two have completely different
	// fixes: get the right bundle, versus the bundle is damaged.
	ErrWrongRecipient = errors.New("bundle: sealed for a different Vesta instance")

	// ErrBadBundle covers a malformed envelope, a truncated field, or a failed
	// authentication tag. Those are deliberately indistinguishable.
	ErrBadBundle = errors.New("bundle: bundle is malformed or was tampered with")
)

// GenerateIdentity creates an instance's long-lived X25519 keypair.
func GenerateIdentity() (*ecdh.PrivateKey, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("bundle: generating identity: %w", err)
	}
	return priv, nil
}

// FormatPublicKey renders a public key for an operator to copy between instances.
func FormatPublicKey(pub *ecdh.PublicKey) string {
	return PublicKeyPrefix + base64.StdEncoding.EncodeToString(pub.Bytes())
}

// ParsePublicKey reverses FormatPublicKey, tolerating surrounding whitespace because
// these keys arrive by way of chat clients and copy-paste.
func ParsePublicKey(s string) (*ecdh.PublicKey, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, PublicKeyPrefix) {
		return nil, fmt.Errorf("bundle: key must start with %q", PublicKeyPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, PublicKeyPrefix))
	if err != nil {
		return nil, errors.New("bundle: key is not valid base64")
	}
	pub, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, errors.New("bundle: key is not a valid X25519 public key")
	}
	return pub, nil
}

// ParsePrivateKey rebuilds an identity from its stored raw bytes.
func ParsePrivateKey(raw []byte) (*ecdh.PrivateKey, error) {
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, errors.New("bundle: stored instance identity is not a valid X25519 private key")
	}
	return priv, nil
}

// Fingerprint is a short, stable name for a public key: the first 8 bytes of its SHA-256,
// hex, space-grouped. Short enough to read aloud when confirming a key out of band, and
// it is not a secret.
func Fingerprint(pub *ecdh.PublicKey) string {
	sum := sha256.Sum256(pub.Bytes())
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + " " + h[4:8] + " " + h[8:12] + " " + h[12:16]
}

// deriveKey performs the ECDH and stretches the shared secret into an AES-256 key.
// Both public keys go into the info string so a key is only ever valid for exactly the
// pair it was derived from.
func deriveKey(priv *ecdh.PrivateKey, peer *ecdh.PublicKey, ephemeralPub, recipientPub []byte) ([]byte, error) {
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("bundle: key agreement failed: %w", err)
	}
	info := infoBase + string(ephemeralPub) + string(recipientPub)
	key, err := hkdf.Key(sha256.New, shared, nil, info, 32)
	if err != nil {
		return nil, fmt.Errorf("bundle: deriving key: %w", err)
	}
	return key, nil
}

// Seal encrypts payload so that only the holder of recipient's private key can read it.
//
// A fresh ephemeral keypair per call means sealing the same project twice produces two
// unrelated ciphertexts, so a bundle cannot be used to prove that two exports had the
// same contents.
func Seal(recipient *ecdh.PublicKey, payload *Payload) (*Envelope, error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("bundle: encoding payload: %w", err)
	}

	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("bundle: generating ephemeral key: %w", err)
	}

	key, err := deriveKey(ephemeral, recipient, ephemeral.PublicKey().Bytes(), recipient.Bytes())
	if err != nil {
		return nil, err
	}

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("bundle: generating nonce: %w", err)
	}

	return &Envelope{
		Version:            Version,
		ExportedAt:         time.Now().UTC().Format(time.RFC3339),
		Recipient:          Fingerprint(recipient),
		EphemeralPublicKey: base64.StdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Nonce:              base64.StdEncoding.EncodeToString(nonce),
		Ciphertext:         base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plaintext, []byte(aad))),
	}, nil
}

// Open reverses Seal using this instance's identity.
func Open(priv *ecdh.PrivateKey, env *Envelope) (*Payload, error) {
	if env == nil {
		return nil, ErrBadBundle
	}
	if env.Version != Version {
		return nil, fmt.Errorf("bundle: unsupported bundle version %d, this instance understands version %d", env.Version, Version)
	}

	// Checked before any crypto so a misdirected bundle gets a useful answer rather than
	// an authentication failure that looks like corruption.
	if env.Recipient != Fingerprint(priv.PublicKey()) {
		return nil, ErrWrongRecipient
	}

	ephemeralRaw, err := base64.StdEncoding.DecodeString(env.EphemeralPublicKey)
	if err != nil {
		return nil, ErrBadBundle
	}
	ephemeralPub, err := ecdh.X25519().NewPublicKey(ephemeralRaw)
	if err != nil {
		return nil, ErrBadBundle
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, ErrBadBundle
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, ErrBadBundle
	}

	key, err := deriveKey(priv, ephemeralPub, ephemeralRaw, priv.PublicKey().Bytes())
	if err != nil {
		return nil, ErrBadBundle
	}

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, ErrBadBundle
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, ErrBadBundle
	}

	var payload Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, ErrBadBundle
	}
	return &payload, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	return gcm, nil
}
