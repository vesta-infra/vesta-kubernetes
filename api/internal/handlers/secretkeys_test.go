package handlers

import (
	"strings"
	"testing"
)

func TestValidateSecretKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"plain name", "DATABASE_URL", false},
		{"dots and dashes", "tls.crt-2", false},
		{"digits only", "1", false},
		{"empty", "", true},
		{"dot", ".", true},
		{"dot dot", "..", true},
		{"slash", "config/key", true},
		{"space", "MY KEY", true},
		{"plus and slash from base64", "MIIEvQ+ab/cd", true},
		{"at length limit", strings.Repeat("a", 253), false},
		{"one over the limit", strings.Repeat("a", 254), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSecretKey(c.key)
			if (err != nil) != c.wantErr {
				t.Errorf("validateSecretKey(%q) error = %v, wantErr %v", c.key, err, c.wantErr)
			}
		})
	}
}

// A pasted private key must not be echoed back in full: the error travels into API
// responses and server logs.
func TestValidateSecretKeyTruncatesLongKeys(t *testing.T) {
	key := strings.Repeat("Zm9vYmFy", 60) // 480 chars, base64-looking
	err := validateSecretKey(key)
	if err == nil {
		t.Fatal("expected an error for an over-long key")
	}
	if strings.Contains(err.Error(), key) {
		t.Error("error message contains the full key; it must be truncated")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error message should mark the key as truncated, got: %s", err)
	}
}

func TestValidateSecretKeysReportsEveryOffender(t *testing.T) {
	err := validateSecretKeys([]string{"GOOD_KEY", "bad key", "also/bad"})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 invalid secret keys") {
		t.Errorf("expected a count of both offenders, got: %s", msg)
	}
	if !strings.Contains(msg, "bad key") || !strings.Contains(msg, "also/bad") {
		t.Errorf("expected both offending keys named, got: %s", msg)
	}
	if strings.Contains(msg, "GOOD_KEY") {
		t.Errorf("valid key should not be reported, got: %s", msg)
	}
}

func TestValidateSecretDataKeysAcceptsValidMap(t *testing.T) {
	if err := validateSecretDataKeys(map[string]string{"A": "1", "b.c-d_e": "2"}); err != nil {
		t.Errorf("expected valid keys to pass, got: %v", err)
	}
	if err := validateSecretDataKeys(nil); err != nil {
		t.Errorf("expected nil map to pass, got: %v", err)
	}
}
