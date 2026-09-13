package handlers

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBuildHtpasswd(t *testing.T) {
	got, err := buildHtpasswd([]basicAuthUser{
		{Username: "alice", Password: "correct-horse"},
		{Username: "bob", Password: "battery-staple"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), got)
	}

	// The plaintext must not survive anywhere in the file.
	for _, secret := range []string{"correct-horse", "battery-staple"} {
		if strings.Contains(got, secret) {
			t.Errorf("plaintext password %q is present in the htpasswd data", secret)
		}
	}

	// Each line must be a real bcrypt hash that verifies against its password.
	for _, line := range lines {
		username, hash, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("line is not user:hash -- %q", line)
		}
		if !strings.HasPrefix(hash, "$2") {
			t.Errorf("%s is not bcrypt-hashed: %q", username, hash)
		}
		password := map[string]string{"alice": "correct-horse", "bob": "battery-staple"}[username]
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
			t.Errorf("the stored hash for %s does not verify against its password: %v", username, err)
		}
	}
}

func TestValidateBasicAuthUsers(t *testing.T) {
	t.Run("rejects a colon in a username", func(t *testing.T) {
		// htpasswd splits on the first colon, so "ad:min" would authenticate as "ad" --
		// access under a name nobody configured.
		err := validateBasicAuthUsers([]basicAuthUser{{Username: "ad:min", Password: "x"}})
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "colon") {
			t.Errorf("the error should explain why, got: %v", err)
		}
	})

	t.Run("rejects a newline in a username", func(t *testing.T) {
		// A newline would inject an extra htpasswd line, adding an account.
		if err := validateBasicAuthUsers([]basicAuthUser{{Username: "a\nroot", Password: "x"}}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("rejects an empty list", func(t *testing.T) {
		if err := validateBasicAuthUsers(nil); err == nil {
			t.Fatal("expected an error: no users locks everyone out")
		}
	})

	t.Run("rejects a duplicate username", func(t *testing.T) {
		err := validateBasicAuthUsers([]basicAuthUser{
			{Username: "alice", Password: "one"},
			{Username: "alice", Password: "two"},
		})
		if err == nil {
			t.Fatal("expected an error: which password wins is undefined")
		}
	})

	t.Run("rejects a password bcrypt cannot represent", func(t *testing.T) {
		// bcrypt truncates past 72 bytes, so a longer password would appear to work while
		// only its first 72 bytes were ever checked.
		err := validateBasicAuthUsers([]basicAuthUser{{Username: "a", Password: strings.Repeat("x", 73)}})
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("accepts a 72-byte password", func(t *testing.T) {
		if err := validateBasicAuthUsers([]basicAuthUser{{Username: "a", Password: strings.Repeat("x", 72)}}); err != nil {
			t.Errorf("72 bytes is the limit, not past it: %v", err)
		}
	})

	t.Run("requires a password", func(t *testing.T) {
		if err := validateBasicAuthUsers([]basicAuthUser{{Username: "alice"}}); err == nil {
			t.Fatal("expected an error")
		}
	})
}

// Editing a middleware should not require retyping every password. A user resubmitted
// with a blank password keeps the hash already stored.
func TestMergeCredentials(t *testing.T) {
	existing := map[string]string{"alice": "$2y$10$existinghash", "bob": "$2y$10$bobhash"}

	t.Run("a blank password keeps the stored hash", func(t *testing.T) {
		kept, fresh, err := mergeCredentials([]basicAuthUser{{Username: "alice"}}, existing)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(fresh) != 0 {
			t.Errorf("nothing should need hashing, got %v", fresh)
		}
		if len(kept) != 1 || kept[0] != "alice:$2y$10$existinghash" {
			t.Errorf("the existing hash was not kept: %v", kept)
		}
	})

	t.Run("a supplied password is hashed afresh", func(t *testing.T) {
		kept, fresh, err := mergeCredentials([]basicAuthUser{{Username: "alice", Password: "new"}}, existing)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(kept) != 0 || len(fresh) != 1 {
			t.Errorf("expected one password to hash, got kept=%v fresh=%v", kept, fresh)
		}
	})

	t.Run("a new user with no password is an error, not a silent skip", func(t *testing.T) {
		// Silently dropping them would create an account the operator believes exists.
		_, _, err := mergeCredentials([]basicAuthUser{{Username: "carol"}}, existing)
		if err == nil {
			t.Fatal("expected an error for a new user with no password")
		}
	})

	t.Run("a user left out is dropped", func(t *testing.T) {
		// Removing someone is done by not submitting them, so the merge must not
		// resurrect them from the existing hashes.
		kept, fresh, err := mergeCredentials([]basicAuthUser{{Username: "alice"}}, existing)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		all := append(append([]string{}, kept...), func() []string {
			out := []string{}
			for _, f := range fresh {
				out = append(out, f.Username)
			}
			return out
		}()...)
		for _, line := range all {
			if strings.HasPrefix(line, "bob") {
				t.Errorf("bob was removed but survived the merge: %v", all)
			}
		}
	})
}

func TestCredentialsSecretName(t *testing.T) {
	if got := credentialsSecretName("office-only"); got != "vmw-office-only-auth" {
		t.Errorf("got %q", got)
	}
}

func TestBasicAuthValidationAcceptsBothConfigurationStyles(t *testing.T) {
	t.Run("inline users are accepted", func(t *testing.T) {
		err := validateMiddlewarePayload(middlewareRequest{
			Type:   "basicAuth",
			Config: map[string]interface{}{"users": []interface{}{map[string]interface{}{"username": "a", "password": "b"}}},
		})
		if err != nil {
			t.Errorf("entering credentials should be allowed: %v", err)
		}
	})

	t.Run("a self-managed secret is accepted", func(t *testing.T) {
		err := validateMiddlewarePayload(middlewareRequest{
			Type: "basicAuth", Config: map[string]interface{}{"secretName": "my-htpasswd"},
		})
		if err != nil {
			t.Errorf("naming your own Secret should still work: %v", err)
		}
	})

	t.Run("both at once is refused", func(t *testing.T) {
		// Preferring one silently would leave the other looking applied.
		err := validateMiddlewarePayload(middlewareRequest{
			Type: "basicAuth",
			Config: map[string]interface{}{
				"secretName": "my-htpasswd",
				"users":      []interface{}{map[string]interface{}{"username": "a", "password": "b"}},
			},
		})
		if err == nil {
			t.Error("expected an error when both are set")
		}
	})

	t.Run("neither is refused", func(t *testing.T) {
		if err := validateMiddlewarePayload(middlewareRequest{
			Type: "basicAuth", Config: map[string]interface{}{"realm": "staging"},
		}); err == nil {
			t.Error("expected an error when neither is set")
		}
	})
}
