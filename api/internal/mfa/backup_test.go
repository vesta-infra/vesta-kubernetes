package mfa

import (
	"strings"
	"testing"
)

func TestGenerateBackupCodes(t *testing.T) {
	codes, err := GenerateBackupCodes()
	if err != nil {
		t.Fatalf("GenerateBackupCodes: %v", err)
	}
	if len(codes) != BackupCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), BackupCodeCount)
	}

	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate code %q: a user would have fewer usable codes than they think", c)
		}
		seen[c] = true

		normalized := NormalizeBackupCode(c)
		if len(normalized) != BackupCodeLength {
			t.Errorf("code %q normalises to %d chars, want %d", c, len(normalized), BackupCodeLength)
		}
		for _, r := range normalized {
			if !strings.ContainsRune(backupAlphabet, r) {
				t.Errorf("code %q contains %q, which is outside the alphabet", c, r)
			}
		}
		if !ValidBackupCodeFormat(c) {
			t.Errorf("generated code %q fails its own format check", c)
		}
	}
}

// The alphabet deliberately omits I, L, O and U so codes survive being written down.
func TestGeneratedCodesAvoidAmbiguousCharacters(t *testing.T) {
	codes, err := GenerateBackupCodes()
	if err != nil {
		t.Fatalf("GenerateBackupCodes: %v", err)
	}
	for _, c := range codes {
		for _, bad := range []string{"I", "L", "O", "U"} {
			if strings.Contains(NormalizeBackupCode(c), bad) {
				t.Errorf("code %q contains ambiguous character %q", c, bad)
			}
		}
	}
}

func TestNormalizeBackupCode(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"already canonical", "ABCD1234EFGH5678", "ABCD1234EFGH5678"},
		{"lowercase", "abcd1234efgh5678", "ABCD1234EFGH5678"},
		{"display dashes stripped", "ABCD-1234-EFGH-5678", "ABCD1234EFGH5678"},
		{"spaces stripped", "ABCD 1234 EFGH 5678", "ABCD1234EFGH5678"},
		{"underscores stripped", "ABCD_1234_EFGH_5678", "ABCD1234EFGH5678"},
		{"surrounding whitespace", "  ABCD1234EFGH5678\n", "ABCD1234EFGH5678"},
		{"letter I read as one", "IBCD1234EFGH5678", "1BCD1234EFGH5678"},
		{"letter L read as one", "LBCD1234EFGH5678", "1BCD1234EFGH5678"},
		{"letter O read as zero", "OBCD1234EFGH5678", "0BCD1234EFGH5678"},
		{"mixed confusables and separators", "o-i l 1234efgh5678", "0111234EFGH5678"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeBackupCode(c.input); got != c.want {
				t.Errorf("NormalizeBackupCode(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestValidBackupCodeFormat(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"canonical", "ABCD1234EFGH5678", true},
		{"with dashes", "ABCD-1234-EFGH-5678", true},
		{"lowercase", "abcd1234efgh5678", true},
		{"too short", "ABCD1234", false},
		{"too long", "ABCD1234EFGH5678X", false},
		{"empty", "", false},
		{"a six digit totp code", "123456", false},
		{"contains U which is not in the alphabet", "UBCD1234EFGH5678", false},
		{"punctuation", "ABCD!234EFGH5678", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidBackupCodeFormat(c.input); got != c.want {
				t.Errorf("ValidBackupCodeFormat(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// The verify endpoint takes one field and decides what it was given, so a TOTP code must
// never look like a backup code.
func TestTOTPCodeIsNotMistakenForABackupCode(t *testing.T) {
	for _, code := range []string{"000000", "123456", "999999"} {
		if ValidBackupCodeFormat(code) {
			t.Errorf("TOTP code %q was classified as a backup code", code)
		}
	}
}
