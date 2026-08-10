package controllers

import (
	"fmt"
	"regexp"
)

// Kubernetes rejects Secret keys longer than 253 characters or containing anything
// outside [-._a-zA-Z0-9]. A single bad key fails the write for the entire Secret, so a
// VestaSecret carrying one would otherwise block every other key in it from syncing and
// requeue forever. Validate here and skip the offender instead.
const maxSecretKeyLength = 253

var secretKeyPattern = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

func validSecretKey(key string) bool {
	if key == "" || len(key) > maxSecretKeyLength || key == "." || key == ".." {
		return false
	}
	return secretKeyPattern.MatchString(key)
}

// describeSecretKey truncates a key before it reaches status or logs. An over-long key is
// almost always a value pasted into the key field, so echoing it whole would copy secret
// material into log sinks on every reconcile.
func describeSecretKey(key string) string {
	const shown = 24
	runes := []rune(key)
	if len(runes) <= shown {
		return key
	}
	return fmt.Sprintf("%s… (%d chars, truncated)", string(runes[:shown]), len(runes))
}
