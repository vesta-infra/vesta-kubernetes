package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Replace this binary with the latest release",
	Long: `Download the newest published CLI and replace this binary in place.

Does the same job as re-running install.sh, without needing curl or the install script.
The download is checksum-verified against the release's checksums.txt before anything is
replaced.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _ := cmd.Flags().GetString("version")
		force, _ := cmd.Flags().GetBool("force")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if isDevBuild() && !force {
			return fmt.Errorf("this is a development build (version %q); use --force to replace it anyway", version)
		}

		if target == "" {
			fmt.Println("Checking for the latest release...")
			latest, err := latestRelease()
			if err != nil {
				return err
			}
			target = latest
		}
		target = strings.TrimPrefix(target, "v")

		if target == version && !force {
			fmt.Printf("Already on %s.\n", version)
			return nil
		}
		if !isDevBuild() && !isNewer(version, target) && !force {
			// Going backwards is a legitimate thing to want, but not by accident.
			return fmt.Errorf("%s is not newer than the installed %s; use --force to install it anyway", target, version)
		}

		// The running binary, resolved through any symlink -- replacing the symlink
		// rather than its target would leave the real binary stale and the next run
		// unchanged.
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locating this binary: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}

		archive := releaseAssetName(target)
		url := fmt.Sprintf("%s/v%s/%s", downloadBase, target, archive)

		if dryRun {
			fmt.Printf("Would download %s\n", url)
			fmt.Printf("Would replace  %s\n", exe)
			return nil
		}

		fmt.Printf("Downloading vesta %s for %s/%s...\n", target, runtime.GOOS, runtime.GOARCH)
		payload, err := fetch(url)
		if err != nil {
			return err
		}

		if err := verifyChecksum(target, archive, payload); err != nil {
			return err
		}
		fmt.Println("Checksum verified.")

		binary, err := extractBinary(payload)
		if err != nil {
			return err
		}

		if err := replaceBinary(exe, binary); err != nil {
			return err
		}

		fmt.Printf("Updated %s to %s.\n", exe, target)
		return nil
	},
}

// releaseAssetName matches the archive names `make cli-release` produces.
func releaseAssetName(v string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("vesta_%s_%s_%s.zip", v, runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("vesta_%s_%s_%s.tar.gz", v, runtime.GOOS, runtime.GOARCH)
}

// verifyChecksum checks the download against the release's published checksums.
//
// This runs before anything touches the installed binary. A truncated download or a
// tampered mirror should fail here, not after the running CLI has been overwritten.
func verifyChecksum(version, archive string, payload []byte) error {
	sums, err := fetch(fmt.Sprintf("%s/v%s/checksums.txt", downloadBase, version))
	if err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}

	return checkAgainstSums(string(sums), archive, payload)
}

// checkAgainstSums is the comparison half of verifyChecksum, split out so it can be
// tested without reaching the network.
func checkAgainstSums(sums, archive string, payload []byte) error {
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])

	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// checksums.txt entries look like "<sha256>  vesta_0.7.0_darwin_arm64.tar.gz",
		// and shasum prefixes the name with "*" in binary mode.
		if strings.TrimPrefix(fields[1], "*") != archive {
			continue
		}
		if fields[0] != want {
			return fmt.Errorf("checksum mismatch for %s: refusing to install", archive)
		}
		return nil
	}
	return fmt.Errorf("no checksum published for %s", archive)
}

// extractBinary pulls the vesta binary out of a release tarball.
func extractBinary(payload []byte) ([]byte, error) {
	if runtime.GOOS == "windows" {
		// The Windows asset is a zip; self-update does not support it yet rather than
		// pretending to and producing a corrupt binary.
		return nil, fmt.Errorf("self-update does not support Windows yet; download the .zip from https://github.com/%s/releases", githubRepo)
	}

	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("reading archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != "vesta" {
			continue
		}
		// Bounded read: a hostile archive should not be able to exhaust memory here.
		return io.ReadAll(io.LimitReader(tr, 200<<20))
	}
	return nil, fmt.Errorf("no vesta binary found in the release archive")
}

// replaceBinary swaps the new binary in atomically.
//
// Written to a temporary file in the same directory and renamed, rather than truncating
// and rewriting in place: a rename is atomic, so an interrupted update leaves the old
// working binary rather than a half-written one. Same directory because rename cannot
// cross filesystems.
func replaceBinary(path string, content []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".vesta-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot write to %s: re-run with sudo, or set VESTA_INSTALL_DIR to a writable directory and reinstall", dir)
		}
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("cannot replace %s: re-run with sudo", path)
		}
		return err
	}
	return nil
}

func init() {
	selfUpdateCmd.Flags().String("version", "", "Install a specific version instead of the latest")
	selfUpdateCmd.Flags().Bool("force", false, "Install even if it is not newer, or over a dev build")
	selfUpdateCmd.Flags().Bool("dry-run", false, "Show what would be downloaded and replaced")
	rootCmd.AddCommand(selfUpdateCmd)
}
