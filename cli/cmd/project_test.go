package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRecipientKeyAcceptsInlineValue(t *testing.T) {
	got, err := readRecipientKey("  vesta1:pub:abc123  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "vesta1:pub:abc123" {
		t.Fatalf("key = %q, want the trimmed inline value", got)
	}
}

// Keys are long enough that operators keep them in a file rather than a shell argument,
// where a truncated paste would otherwise fail deep inside the API.
func TestReadRecipientKeyReadsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.pub")
	if err := os.WriteFile(path, []byte("vesta1:pub:fromfile\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	got, err := readRecipientKey("@" + path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "vesta1:pub:fromfile" {
		t.Fatalf("key = %q, want the file contents without its trailing newline", got)
	}
}

func TestReadRecipientKeyRejectsEmpty(t *testing.T) {
	if _, err := readRecipientKey(""); err == nil {
		t.Fatal("expected an empty recipient key to be rejected")
	}
}

func TestReadRecipientKeyReportsMissingFile(t *testing.T) {
	if _, err := readRecipientKey("@/nonexistent/peer.pub"); err == nil {
		t.Fatal("expected a missing key file to be reported")
	}
}
