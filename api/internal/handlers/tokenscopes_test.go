package handlers

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateTokenScopes(t *testing.T) {
	cases := []struct {
		name     string
		reqested []string
		role     string
		want     []string
		wantErr  bool
	}{
		{"default for developer", nil, "developer", []string{"deploy", "read"}, false},
		{"default for viewer is read only", nil, "viewer", []string{"read"}, false},
		{"empty slice falls back to default", []string{}, "developer", []string{"deploy", "read"}, false},
		{"blank entries ignored", []string{"read", "  "}, "developer", []string{"read"}, false},
		{"normalises case and whitespace", []string{" Read ", "DEPLOY"}, "developer", []string{"read", "deploy"}, false},
		{"de-duplicates", []string{"read", "read"}, "developer", []string{"read"}, false},
		{"admin may request admin", []string{"admin"}, "admin", []string{"admin"}, false},
		{"developer may not request admin", []string{"admin"}, "developer", nil, true},
		{"admin scope rejected despite odd casing", []string{"AdMiN"}, "developer", nil, true},
		{"viewer may not request write", []string{"write"}, "viewer", nil, true},
		{"viewer may not request deploy", []string{"deploy"}, "viewer", nil, true},
		{"unknown scope rejected", []string{"superuser"}, "admin", nil, true},
		{"one bad scope rejects the whole request", []string{"read", "superuser"}, "admin", nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := validateTokenScopes(c.reqested, c.role)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateTokenScopes(%v, %q) error = %v, wantErr %v", c.reqested, c.role, err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("validateTokenScopes(%v, %q) = %v, want %v", c.reqested, c.role, got, c.want)
			}
		})
	}
}

// middleware.RequireScope treats the "admin" scope as satisfying every scope check, so a
// non-admin being able to request it was a privilege escalation, not a cosmetic issue.
func TestValidateTokenScopesBlocksPrivilegeEscalation(t *testing.T) {
	for _, role := range []string{"developer", "viewer", ""} {
		if _, err := validateTokenScopes([]string{"admin"}, role); err == nil {
			t.Errorf("role %q was allowed to mint an admin-scoped token", role)
		}
	}
}

// The defaults are returned to callers and must not be aliased, or one request mutating
// its scope slice would change what every later request gets.
func TestValidateTokenScopesDoesNotAliasDefaults(t *testing.T) {
	got, err := validateTokenScopes(nil, "developer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got[0] = "mutated"
	if defaultAPITokenScopes[0] == "mutated" {
		t.Error("mutating the returned slice changed the package-level defaults")
	}
}

func TestValidateTokenScopesErrorNamesValidScopes(t *testing.T) {
	_, err := validateTokenScopes([]string{"superuser"}, "admin")
	if err == nil {
		t.Fatal("expected an error for an unknown scope")
	}
	for _, s := range validAPITokenScopes {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error should list valid scope %q, got: %s", s, err)
		}
	}
}
