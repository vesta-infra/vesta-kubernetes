package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kubernetes.getvesta.sh/api/internal/db"
)

const (
	releasesURL      = "https://api.github.com/repos/vesta-infra/vesta-kubernetes/releases/latest"
	releaseTagURL    = "https://api.github.com/repos/vesta-infra/vesta-kubernetes/releases/tags/v%s"
	updateCheckEvery = 24 * time.Hour
)

// updateHTTPClient has a short timeout: this runs on a ticker in the background and a
// hung connection must not pin a goroutine for the life of the process.
var updateHTTPClient = &http.Client{Timeout: 15 * time.Second}

// FetchLatestRelease returns the newest published release version, without the "v".
func FetchLatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Unauthenticated GitHub API calls are rate-limited per IP, and a whole cluster
		// behind one NAT can hit it. Say so plainly rather than reporting "no updates".
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return "", fmt.Errorf("update check rate-limited by GitHub, try again later")
		}
		return "", fmt.Errorf("update check failed with status %d", resp.StatusCode)
	}

	var release struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding release: %w", err)
	}
	if release.Draft || release.Prerelease {
		return "", nil
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}

// ReleaseExists reports whether a specific version was published, so a typo is caught
// before an upgrade Job is created rather than inside helm.
func ReleaseExists(ctx context.Context, version string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(releaseTagURL, version), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		// An air-gapped cluster cannot reach GitHub, and refusing every upgrade there
		// would make the feature useless. The caller treats an error as "cannot say".
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// IsNewerVersion compares two semver-ish versions numerically.
//
// Field-by-field rather than lexicographically, because string comparison puts "0.10.0"
// before "0.9.0" and would hide exactly the upgrade a user most wants to see.
func IsNewerVersion(current, candidate string) bool {
	c := parseVersion(current)
	n := parseVersion(candidate)
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
	// Drop any pre-release or build suffix; only the numeric core is ordered here.
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

// UpdateChecker refreshes the newest-known-release setting in the background.
type UpdateChecker struct {
	DB *db.DB
}

// Start polls until the context is cancelled. It checks once at startup so a fresh
// install does not sit for a day before it can say anything about updates.
func (u *UpdateChecker) Start(ctx context.Context) {
	u.checkOnce(ctx)

	ticker := time.NewTicker(updateCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.checkOnce(ctx)
		}
	}
}

func (u *UpdateChecker) checkOnce(ctx context.Context) {
	if !u.DB.GetBoolSetting(ctx, db.SettingUpdateCheckEnabled, true) {
		return
	}
	latest, err := FetchLatestRelease(ctx)
	if err != nil {
		// Not being able to reach GitHub is unremarkable -- an air-gapped cluster will
		// fail this every time -- so it is logged once and never surfaced as an alert.
		log.Printf("update check: %v", err)
		return
	}
	if latest == "" {
		return
	}
	if err := u.DB.RecordUpdateCheck(ctx, latest); err != nil {
		log.Printf("update check: recording result: %v", err)
	}
}
