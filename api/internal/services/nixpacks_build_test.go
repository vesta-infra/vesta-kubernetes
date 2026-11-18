package services

import (
	"strings"
	"testing"
)

// The nixpacks strategy failed with "nixpacks: not found" for as long as it existed.
//
// It ran in ghcr.io/railwayapp/nixpacks, which is the nix base image that built applications
// run ON -- its PATH is nix profiles, its command is bash, and it carries no nixpacks binary.
// Behind that was a second problem: nixpacks shells out to `docker build`, and a pod has no
// daemon, so even with the CLI present the script could not have worked.
func TestNixpacksGeneratesAndKanikoBuilds(t *testing.T) {
	b := &Builder{}
	job, err := b.createBuildJob(BuildRequest{
		Strategy:   BuildStrategyNixpacks,
		Repository: "acme/web",
		Branch:     "main",
		ImageDest:  "registry.example.com/acme/web:abc1234",
	}, "build-test")
	if err != nil {
		t.Fatal(err)
	}

	pod := job.Spec.Template.Spec
	if len(pod.InitContainers) != 1 {
		t.Fatalf("init containers = %d, want the generate step", len(pod.InitContainers))
	}

	gen := pod.InitContainers[0]
	if strings.Contains(gen.Image, "railwayapp") {
		t.Errorf("generating in %q; that image has no nixpacks binary", gen.Image)
	}
	script := strings.Join(gen.Args, "\n")
	if !strings.Contains(script, "--out") {
		t.Error("nixpacks is not asked to write a Dockerfile, so it would try to build — " +
			"which needs a docker daemon the pod does not have")
	}
	if strings.Contains(script, "crane push") {
		t.Error("the script still calls crane push, which takes a tarball and a destination, " +
			"not an image pushed to itself")
	}

	// And the build itself has to be kaniko, against the generated file rather than the
	// repository, because the Dockerfile does not exist in the repository.
	build := pod.Containers[0]
	if !strings.Contains(build.Image, "kaniko") {
		t.Errorf("build image = %q, want kaniko", build.Image)
	}
	args := strings.Join(build.Args, " ")
	if !strings.Contains(args, "/workspace/.nixpacks/Dockerfile") {
		t.Errorf("kaniko is not pointed at the generated Dockerfile: %s", args)
	}
	if strings.Contains(args, "git://") {
		t.Error("kaniko is using a git context; the generated Dockerfile is not in the repo")
	}
	if !strings.Contains(args, "registry.example.com/acme/web:abc1234") {
		t.Error("no destination")
	}
}

// Both containers need the workspace, or the Dockerfile written by one is invisible to the
// other and kaniko fails on a path that was there a second ago.
func TestNixpacksStepsShareTheWorkspace(t *testing.T) {
	b := &Builder{}
	job, _ := b.createBuildJob(BuildRequest{
		Strategy: BuildStrategyNixpacks, Repository: "acme/web", ImageDest: "r/x:1",
	}, "build-test")

	pod := job.Spec.Template.Spec

	has := func(name string, paths []string) bool {
		for _, p := range paths {
			if p == "/workspace" {
				return true
			}
		}
		t.Errorf("%s does not mount /workspace", name)
		return false
	}

	var genPaths, buildPaths []string
	for _, m := range pod.InitContainers[0].VolumeMounts {
		genPaths = append(genPaths, m.MountPath)
	}
	for _, m := range pod.Containers[0].VolumeMounts {
		buildPaths = append(buildPaths, m.MountPath)
	}
	has("the generate step", genPaths)
	has("the build step", buildPaths)

	var found bool
	for _, v := range pod.Volumes {
		if v.Name == "workspace" {
			found = true
		}
	}
	if !found {
		t.Error("no workspace volume on the pod")
	}
}

// The strategies that do not prepare anything must not gain an init container.
func TestOtherStrategiesHaveNoInitContainer(t *testing.T) {
	b := &Builder{}
	for _, strategy := range []string{BuildStrategyDockerfile, BuildStrategyBuildpacks} {
		job, err := b.createBuildJob(BuildRequest{
			Strategy: strategy, Repository: "acme/web", Branch: "main",
			Dockerfile: "Dockerfile", ImageDest: "r/x:1",
		}, "build-test")
		if err != nil {
			t.Fatalf("%s: %v", strategy, err)
		}
		if n := len(job.Spec.Template.Spec.InitContainers); n != 0 {
			t.Errorf("%s gained %d init containers", strategy, n)
		}
	}
}
