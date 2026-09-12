package mfa

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const (
	// BackupCodeCount is how many single-use codes an enrollment produces.
	BackupCodeCount = 10

	// BackupCodeLength is 16 characters over a 32-symbol alphabet, so 80 bits.
	//
	// The length is driven by how these are stored. Codes are hashed with the same
	// unsalted SHA-256 the codebase already uses for API tokens, which is sound only
	// because the input has enough entropy that a precomputed table is hopeless. At 10
	// characters (50 bits) that assumption gets uncomfortable against a GPU; 80 bits is
	// far beyond reach. A slow KDF would be the alternative, but it would cost a linear
	// scan of every stored code on each attempt instead of one indexed lookup.
	BackupCodeLength = 16

	// backupAlphabet is Crockford base32: no I, L, O or U, so the codes survive being
	// read aloud, written down and retyped.
	backupAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	// backupCodeGroup inserts a separator every N characters for legibility. The
	// separator is cosmetic and stripped on input.
	backupCodeGroup = 4
)

// GenerateBackupCodes returns fresh single-use recovery codes, formatted for display.
// They are shown to the user exactly once and stored only as hashes.
func GenerateBackupCodes() ([]string, error) {
	codes := make([]string, 0, BackupCodeCount)
	seen := make(map[string]bool, BackupCodeCount)

	for len(codes) < BackupCodeCount {
		code, err := randomBackupCode()
		if err != nil {
			return nil, err
		}
		if seen[code] {
			// Astronomically unlikely at 80 bits, but a duplicate would silently reduce
			// the number of usable codes.
			continue
		}
		seen[code] = true
		codes = append(codes, formatBackupCode(code))
	}
	return codes, nil
}

func randomBackupCode() (string, error) {
	max := big.NewInt(int64(len(backupAlphabet)))
	var sb strings.Builder
	sb.Grow(BackupCodeLength)
	for i := 0; i < BackupCodeLength; i++ {
		// crypto/rand.Int rather than modulo arithmetic on a random byte, which would
		// bias the alphabet.
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("mfa: generating backup code: %w", err)
		}
		sb.WriteByte(backupAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

func formatBackupCode(code string) string {
	var sb strings.Builder
	for i, r := range code {
		if i > 0 && i%backupCodeGroup == 0 {
			sb.WriteByte('-')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// NormalizeBackupCode canonicalises user input before hashing.
//
// People retype these from paper, so the separators, case and whitespace they produce
// vary. The characters Crockford base32 omits are folded onto the ones they are mistaken
// for - I and L read as 1, O as 0 - so a plausible misreading still works.
func NormalizeBackupCode(s string) string {
	var sb strings.Builder
	sb.Grow(BackupCodeLength)
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		switch r {
		case '-', ' ', '\t', '_':
			continue
		case 'I', 'L':
			sb.WriteByte('1')
		case 'O':
			sb.WriteByte('0')
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// ValidBackupCodeFormat reports whether input could be a backup code at all. It lets the
// verify endpoint tell a six-digit TOTP code apart from a recovery code without asking
// the client which one it is sending.
func ValidBackupCodeFormat(s string) bool {
	n := NormalizeBackupCode(s)
	if len(n) != BackupCodeLength {
		return false
	}
	for _, r := range n {
		if !strings.ContainsRune(backupAlphabet, r) {
			return false
		}
	}
	return true
}
