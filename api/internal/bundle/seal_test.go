package bundle

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func samplePayload() *Payload {
	return &Payload{
		Project: ProjectEntry{Name: "acme", Spec: map[string]interface{}{"displayName": "Acme"}},
		Apps:    []AppEntry{{Name: "api", Spec: map[string]interface{}{"project": "acme"}}},
		Secrets: map[string]map[string]map[string]string{
			"api": {"staging": {"DATABASE_URL": "postgres://u:p@h/db"}},
		},
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	recipient, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("generating identity: %v", err)
	}

	env, err := Seal(recipient.PublicKey(), samplePayload())
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	got, err := Open(recipient, env)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if got.Project.Name != "acme" {
		t.Errorf("project name = %q, want %q", got.Project.Name, "acme")
	}
	if got.Secrets["api"]["staging"]["DATABASE_URL"] != "postgres://u:p@h/db" {
		t.Errorf("secret did not survive the round trip: %#v", got.Secrets)
	}
}

// The whole point of the feature: a bundle carried to the wrong instance stays shut.
func TestOpenWithDifferentInstanceKeyReportsWrongRecipient(t *testing.T) {
	recipient, _ := GenerateIdentity()
	stranger, _ := GenerateIdentity()

	env, err := Seal(recipient.PublicKey(), samplePayload())
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	_, err = Open(stranger, env)
	if !errors.Is(err, ErrWrongRecipient) {
		t.Fatalf("err = %v, want ErrWrongRecipient", err)
	}
}

// A stranger who rewrites the recipient fingerprint to get past the cheap check still
// cannot derive the key, so this must fail on the tag rather than succeed.
func TestOpenWithForgedRecipientFingerprintFails(t *testing.T) {
	recipient, _ := GenerateIdentity()
	stranger, _ := GenerateIdentity()

	env, _ := Seal(recipient.PublicKey(), samplePayload())
	env.Recipient = Fingerprint(stranger.PublicKey())

	if _, err := Open(stranger, env); !errors.Is(err, ErrBadBundle) {
		t.Fatalf("err = %v, want ErrBadBundle", err)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	recipient, _ := GenerateIdentity()
	env, _ := Seal(recipient.PublicKey(), samplePayload())

	raw, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		t.Fatalf("decoding ciphertext: %v", err)
	}
	raw[0] ^= 0xff
	env.Ciphertext = base64.StdEncoding.EncodeToString(raw)

	if _, err := Open(recipient, env); !errors.Is(err, ErrBadBundle) {
		t.Fatalf("err = %v, want ErrBadBundle", err)
	}
}

// Sealing twice must not reveal that the two bundles hold the same project.
func TestSealIsNonDeterministic(t *testing.T) {
	recipient, _ := GenerateIdentity()

	first, _ := Seal(recipient.PublicKey(), samplePayload())
	second, _ := Seal(recipient.PublicKey(), samplePayload())

	if first.Ciphertext == second.Ciphertext {
		t.Fatal("two seals of the same payload produced identical ciphertext")
	}
	if first.EphemeralPublicKey == second.EphemeralPublicKey {
		t.Fatal("ephemeral key was reused across seals")
	}
}

// The bundle travels as a file; nothing about what is inside may be readable from it.
func TestEnvelopeLeaksNoProjectDetail(t *testing.T) {
	recipient, _ := GenerateIdentity()
	env, _ := Seal(recipient.PublicKey(), samplePayload())

	for _, secret := range []string{"acme", "api", "DATABASE_URL", "postgres"} {
		if strings.Contains(env.Ciphertext, secret) || strings.Contains(env.Recipient, secret) {
			t.Errorf("envelope exposes %q in cleartext", secret)
		}
	}
}

func TestPublicKeyRoundTripsThroughText(t *testing.T) {
	identity, _ := GenerateIdentity()
	encoded := FormatPublicKey(identity.PublicKey())

	// Operators paste these out of chat clients, so surrounding whitespace is normal.
	parsed, err := ParsePublicKey("  " + encoded + "\n")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if Fingerprint(parsed) != Fingerprint(identity.PublicKey()) {
		t.Fatal("fingerprint changed across a text round trip")
	}
}

func TestParsePublicKeyRejectsUntaggedKey(t *testing.T) {
	identity, _ := GenerateIdentity()
	raw := base64.StdEncoding.EncodeToString(identity.PublicKey().Bytes())

	if _, err := ParsePublicKey(raw); err == nil {
		t.Fatal("expected an untagged key to be rejected")
	}
}

func TestOpenRejectsUnknownVersion(t *testing.T) {
	recipient, _ := GenerateIdentity()
	env, _ := Seal(recipient.PublicKey(), samplePayload())
	env.Version = Version + 1

	if _, err := Open(recipient, env); err == nil {
		t.Fatal("expected a future bundle version to be rejected")
	}
}
