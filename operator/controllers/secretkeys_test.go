package controllers

import (
	"strings"
	"testing"
)

func TestValidSecretKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"DATABASE_URL", true},
		{"tls.crt", true},
		{"a-b_c.d1", true},
		{"", false},
		{".", false},
		{"..", false},
		{"has space", false},
		{"has/slash", false},
		{"MIIEvQ+ab/cd", false},
		{strings.Repeat("a", 253), true},
		{strings.Repeat("a", 254), false},
	}

	for _, c := range cases {
		if got := validSecretKey(c.key); got != c.want {
			t.Errorf("validSecretKey(%.20q) = %v, want %v", c.key, got, c.want)
		}
	}
}

// Invalid keys reach status and logs on every reconcile, so an over-long key — which is
// usually a pasted secret value — must be truncated first.
func TestDescribeSecretKeyTruncates(t *testing.T) {
	short := "DATABASE_URL"
	if got := describeSecretKey(short); got != short {
		t.Errorf("describeSecretKey(%q) = %q, want it unchanged", short, got)
	}

	long := strings.Repeat("Zm9vYmFy", 60)
	got := describeSecretKey(long)
	if strings.Contains(got, long) {
		t.Error("describeSecretKey returned the full key; it must be truncated")
	}
	if len(got) > 80 {
		t.Errorf("describeSecretKey returned %d chars, want a short summary", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected the summary to mark truncation, got %q", got)
	}
}
