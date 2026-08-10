package handlers

import (
	"fmt"
	"regexp"
	"sort"
)

// Kubernetes rejects Secret keys that are longer than 253 characters or that contain
// anything outside [-._a-zA-Z0-9]. That rejection happens in the operator, against the
// API server, long after the value was accepted here — leaving the VestaSecret stored
// but unsyncable and the reconciler retrying forever. Validate at the write path instead
// so a bad key is a 400 the caller sees immediately.
const maxSecretKeyLength = 253

var secretKeyPattern = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

func validateSecretKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("secret key must not be empty")
	case len(key) > maxSecretKeyLength:
		return fmt.Errorf("secret key %s is %d characters long; keys must be no more than %d "+
			"(a key this long usually means a value was entered in the key field)",
			describeSecretKey(key), len(key), maxSecretKeyLength)
	case key == "." || key == "..":
		return fmt.Errorf("secret key must not be %q", key)
	case !secretKeyPattern.MatchString(key):
		return fmt.Errorf("secret key %s is invalid: keys may contain only letters, digits, '-', '_' and '.'",
			describeSecretKey(key))
	}
	return nil
}

// validateSecretKeys reports every offending key at once, so importing a .env file does
// not turn into one round trip per mistake.
func validateSecretKeys(keys []string) error {
	var bad []string
	for _, k := range keys {
		if err := validateSecretKey(k); err != nil {
			bad = append(bad, err.Error())
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	if len(bad) == 1 {
		return fmt.Errorf("%s", bad[0])
	}
	msg := fmt.Sprintf("%d invalid secret keys:", len(bad))
	for _, b := range bad {
		msg += "\n  - " + b
	}
	return fmt.Errorf("%s", msg)
}

func validateSecretDataKeys(data map[string]string) error {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return validateSecretKeys(keys)
}

func validateSecretDataKeysAny(data map[string]interface{}) error {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return validateSecretKeys(keys)
}

// describeSecretKey truncates before echoing a key back. An over-long key is typically a
// pasted secret value, and the error message travels into API responses and logs.
func describeSecretKey(key string) string {
	const shown = 24
	runes := []rune(key)
	if len(runes) <= shown {
		return fmt.Sprintf("%q", key)
	}
	return fmt.Sprintf("%q (truncated)", string(runes[:shown])+"…")
}
