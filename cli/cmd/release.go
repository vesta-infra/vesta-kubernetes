package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	githubRepo    = "vesta-infra/vesta-kubernetes"
	githubAPIBase = "https://api.github.com/repos/" + githubRepo
	downloadBase  = "https://github.com/" + githubRepo + "/releases/download"
)

// releaseHTTPClient has a timeout because a CLI that hangs on a network call with no
// output is indistinguishable from a CLI that has crashed.
var releaseHTTPClient = &http.Client{Timeout: 30 * time.Second}

// latestRelease returns the newest published release version, without the leading "v".
func latestRelease() (string, error) {
	req, err := http.NewRequest(http.MethodGet, githubAPIBase+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := releaseHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		// Unauthenticated GitHub calls are rate-limited per IP, and a whole office behind
		// one address can exhaust it. Say which it is rather than "no releases found".
		return "", fmt.Errorf("rate-limited by GitHub, try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checking for releases: unexpected status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding release: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("no published releases found")
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// isNewer compares two semver-ish versions field by field.
//
// Numerically, not lexicographically: a string compare puts "0.10.0" before "0.9.0" and
// would tell a user on 0.9.0 that they are up to date.
func isNewer(current, candidate string) bool {
	c, n := parseVersion(current), parseVersion(candidate)
	if c == nil || n == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if n[i] != c[i] {
			return n[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i != -1 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// isDevBuild reports whether this binary came from a local build rather than a release.
// Self-update refuses to replace one: overwriting a developer's working binary with a
// published release is almost never what they meant.
func isDevBuild() bool {
	return version == "dev" || version == ""
}

// fetch downloads a URL into memory, for release assets that are a few megabytes.
func fetch(url string) ([]byte, error) {
	resp, err := releaseHTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
