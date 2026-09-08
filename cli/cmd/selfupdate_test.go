package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The name has to match what `make cli-release` produces, or self-update 404s.
func TestReleaseAssetNameMatchesReleaseArtifacts(t *testing.T) {
	got := releaseAssetName("0.7.0")
	want := "vesta_0.7.0_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".zip"
	} else {
		want += ".tar.gz"
	}
	if got != want {
		t.Fatalf("releaseAssetName = %q, want %q", got, want)
	}
}

func tarball(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(content)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractBinaryFindsVesta(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar extraction is not the Windows path")
	}
	got, err := extractBinary(tarball(t, "vesta", []byte("binary-contents")))
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != "binary-contents" {
		t.Fatalf("extracted %q", got)
	}
}

// A release archive that does not contain the binary must fail loudly rather than
// replacing the installed CLI with whatever else was inside.
func TestExtractBinaryRejectsArchiveWithoutVesta(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar extraction is not the Windows path")
	}
	if _, err := extractBinary(tarball(t, "README.md", []byte("not a binary"))); err == nil {
		t.Fatal("expected an archive with no vesta binary to be rejected")
	}
}

// The checksum gate runs before anything touches the installed binary. A tampered or
// truncated download has to fail here.
func TestVerifyChecksumDetectsMismatch(t *testing.T) {
	payload := []byte("the real download")
	sum := sha256.Sum256(payload)

	// Matching entry, wrong payload.
	sums := hex.EncodeToString(sum[:]) + "  vesta_0.7.0_linux_amd64.tar.gz\n"
	if err := checkAgainstSums(sums, "vesta_0.7.0_linux_amd64.tar.gz", []byte("tampered")); err == nil {
		t.Fatal("a payload not matching its published checksum must be rejected")
	}
	if err := checkAgainstSums(sums, "vesta_0.7.0_linux_amd64.tar.gz", payload); err != nil {
		t.Fatalf("the correct payload must verify: %v", err)
	}
}

// shasum writes "*name" in binary mode while sha256sum writes "name"; the Makefile uses
// whichever exists, so both forms appear in published checksums.txt files.
func TestVerifyChecksumAcceptsBinaryModeMarker(t *testing.T) {
	payload := []byte("x")
	sum := sha256.Sum256(payload)
	sums := hex.EncodeToString(sum[:]) + " *vesta_0.7.0_linux_amd64.tar.gz\n"
	if err := checkAgainstSums(sums, "vesta_0.7.0_linux_amd64.tar.gz", payload); err != nil {
		t.Fatalf("binary-mode checksum lines must parse: %v", err)
	}
}

func TestVerifyChecksumRejectsMissingEntry(t *testing.T) {
	if err := checkAgainstSums("abc  other-file.tar.gz\n", "vesta_0.7.0_linux_amd64.tar.gz", []byte("x")); err == nil {
		t.Fatal("an asset with no published checksum must be rejected")
	}
}

// An interrupted update must leave the old working binary rather than a truncated one,
// which is why the new bytes land in a temp file and are renamed into place.
func TestReplaceBinaryIsAtomicAndExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vesta")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(path, []byte("new")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("replaced binary is not executable")
	}

	// No temp files left behind on success.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".vesta-update-") {
			t.Errorf("temp file %s was not cleaned up", e.Name())
		}
	}
}
