// Package version carries the build identity of the API binary.
//
// Until now the API had none: the image tag on the pod was the only evidence of what was
// running, and nothing served it. That is fine until something wants to compare the
// running version against the latest release, at which point "what am I?" has to have an
// answer that does not involve reading Kubernetes.
package version

import "runtime"

// Injected at build time via -ldflags, the same way cli/cmd/version.go is. The defaults
// are what a local `go build` produces, and are deliberately not a plausible-looking
// version number -- an update check must never mistake a dev build for a release.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info describes this build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// IsRelease reports whether this build came from a tagged release. Update checks use it
// to stay quiet on development builds, where comparing against the newest published
// release would produce a permanent and meaningless "update available".
func IsRelease() bool {
	return Version != "dev" && Version != ""
}
