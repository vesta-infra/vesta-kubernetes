package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// The single most important property of this feature.
//
// Every app that exists today runs with no security context at all. If an upgrade starts
// applying one by default, images that run as root or write to their own filesystem stop
// working -- and they stop working at the next reconcile, across the whole instance, with no
// deploy to correlate it against.
func TestDefaultProfileChangesNothing(t *testing.T) {
	for _, name := range []string{"", "legacy", "nonsense", "Restricted", "BASELINE"} {
		t.Run("profile="+name, func(t *testing.T) {
			// Anything not an exact known profile has to fall back to legacy rather than
			// being guessed at. A typo in platform config must not harden an instance.
			resolved := ResolveSecurityProfile(name, "")
			if name != "legacy" && resolved != ProfileLegacy {
				t.Fatalf("ResolveSecurityProfile(%q) = %q, want legacy", name, resolved)
			}

			pod, container := SecurityContexts(resolved, []int32{8080})
			if pod != nil || container != nil {
				t.Errorf("profile %q produced a security context; existing apps would change behaviour on upgrade", name)
			}

			spec := &corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}
			before := spec.DeepCopy()
			ApplySecurityProfile(spec, resolved)
			if !equalSpecs(spec, before) {
				t.Error("ApplySecurityProfile mutated a pod spec under the default profile")
			}
		})
	}
}

// An app's own setting wins, so one image that cannot run hardened does not force the whole
// instance back to legacy.
func TestAppOverridesPlatform(t *testing.T) {
	cases := []struct{ platform, app, want string }{
		{"restricted", "", "restricted"},
		{"restricted", "legacy", "legacy"},
		{"legacy", "restricted", "restricted"},
		{"baseline", "restricted", "restricted"},
		{"", "baseline", "baseline"},
		{"", "", "legacy"},
		{"baseline", "nonsense", "baseline"},
	}
	for _, tc := range cases {
		if got := ResolveSecurityProfile(tc.platform, tc.app); got != tc.want {
			t.Errorf("ResolveSecurityProfile(platform=%q, app=%q) = %q, want %q",
				tc.platform, tc.app, got, tc.want)
		}
	}
}

// Baseline exists to be turnable on across an instance. The moment it forces a non-root user
// or a read-only filesystem it stops being that, and the setting gets switched on once,
// breaks something, and is switched off forever.
func TestBaselineDoesNotBreakOrdinaryImages(t *testing.T) {
	pod, container := SecurityContexts(ProfileBaseline, []int32{8080})

	if container.RunAsNonRoot != nil {
		t.Error("baseline forces a non-root user; an image with USER root would refuse to start")
	}
	if container.ReadOnlyRootFilesystem != nil {
		t.Error("baseline forces a read-only root filesystem; anything writing to /tmp would break")
	}

	// What it must do.
	if container.AllowPrivilegeEscalation == nil || *container.AllowPrivilegeEscalation {
		t.Error("baseline allows privilege escalation")
	}
	if container.Capabilities == nil || len(container.Capabilities.Drop) == 0 {
		t.Error("baseline drops no capabilities")
	}
	if pod.SeccompProfile == nil || pod.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("baseline does not set the default seccomp filter")
	}

	// And it must need no extra volumes, since it has no read-only filesystem to work around.
	volumes, mounts := WritableVolumes(ProfileBaseline)
	if len(volumes) != 0 || len(mounts) != 0 {
		t.Error("baseline asked for writable volumes it has no use for")
	}
}

// Dropping ALL capabilities takes NET_BIND_SERVICE with it, and a container binding :80 then
// fails to start -- the image is fine, the config looks right, and the pod simply crashes.
func TestPrivilegedPortsKeepTheCapabilityToBindThem(t *testing.T) {
	cases := []struct {
		name  string
		ports []int32
		want  bool
	}{
		{"port 80", []int32{80}, true},
		{"port 443", []int32{443}, true},
		{"port 1023", []int32{1023}, true},
		{"port 1024", []int32{1024}, false},
		{"port 8080", []int32{8080}, false},
		{"no ports declared", nil, false},
		{"a mix, one privileged", []int32{8080, 80}, true},
		// A zero is "unset", not "port 0" -- granting on it would hand the capability to
		// every app that never declared a port.
		{"an unset port", []int32{0}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, profile := range []string{ProfileBaseline, ProfileRestricted} {
				_, container := SecurityContexts(profile, tc.ports)
				var granted bool
				for _, c := range container.Capabilities.Add {
					if c == "NET_BIND_SERVICE" {
						granted = true
					}
				}
				if granted != tc.want {
					t.Errorf("%s: NET_BIND_SERVICE granted = %v, want %v", profile, granted, tc.want)
				}
				// Whatever is added back, ALL is still dropped first.
				if len(container.Capabilities.Drop) != 1 || container.Capabilities.Drop[0] != "ALL" {
					t.Errorf("%s: capabilities.drop = %v, want [ALL]", profile, container.Capabilities.Drop)
				}
			}
		})
	}
}

// runAsNonRoot on its own only makes the kubelet REFUSE an image whose USER is root or unset,
// turning a hardening setting into a CreateContainerConfigError. Naming a UID runs it instead.
func TestRestrictedNamesAUIDRatherThanOnlyDemandingNonRoot(t *testing.T) {
	pod, container := SecurityContexts(ProfileRestricted, []int32{8080})

	if container.RunAsUser == nil {
		t.Fatal("restricted demands a non-root user without naming one; any image with USER root " +
			"or no USER would fail to start rather than running as the intended user")
	}
	if *container.RunAsUser == 0 {
		t.Error("restricted runs as uid 0")
	}
	if pod.RunAsUser == nil || *pod.RunAsUser != *container.RunAsUser {
		t.Error("the pod and container disagree about which user to run as")
	}
	// fsGroup, or a restricted app with a PVC starts and then cannot write to it -- which
	// looks like an application bug rather than a platform setting.
	if pod.FSGroup == nil {
		t.Error("restricted sets no fsGroup; a mounted volume would not be writable")
	}
}

// A read-only root filesystem is not a hardening setting without these; it is an outage.
func TestRestrictedGivesBackSomewhereToWrite(t *testing.T) {
	volumes, mounts := WritableVolumes(ProfileRestricted)
	if len(volumes) == 0 {
		t.Fatal("restricted mounts a read-only root filesystem and provides nowhere to write")
	}
	if len(volumes) != len(mounts) {
		t.Fatalf("%d volumes for %d mounts", len(volumes), len(mounts))
	}

	paths := map[string]bool{}
	for i, m := range mounts {
		paths[m.MountPath] = true
		if m.Name != volumes[i].Name {
			t.Errorf("mount %q references volume %q, but the volume at that index is %q",
				m.MountPath, m.Name, volumes[i].Name)
		}
		if volumes[i].EmptyDir == nil {
			t.Errorf("volume %q is not an emptyDir", volumes[i].Name)
		}
	}
	if !paths["/tmp"] {
		t.Error("nothing writable at /tmp; almost every runtime writes there")
	}
}

// An app that mounts its own volume at one of these paths means it, and replacing it with an
// emptyDir would quietly discard whatever it was.
func TestAnAppsOwnMountIsNotOverwritten(t *testing.T) {
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:         "app",
			Ports:        []corev1.ContainerPort{{ContainerPort: 8080}},
			VolumeMounts: []corev1.VolumeMount{{Name: "scratch", MountPath: "/tmp"}},
		}},
		Volumes: []corev1.Volume{{Name: "scratch"}},
	}

	ApplySecurityProfile(spec, ProfileRestricted)

	var atTmp []string
	for _, m := range spec.Containers[0].VolumeMounts {
		if m.MountPath == "/tmp" {
			atTmp = append(atTmp, m.Name)
		}
	}
	if len(atTmp) != 1 {
		t.Fatalf("/tmp is mounted %d times (%v); a duplicate mount path is rejected by the API server", len(atTmp), atTmp)
	}
	if atTmp[0] != "scratch" {
		t.Errorf("the app's own /tmp volume was replaced by %q", atTmp[0])
	}

	// The other path still gets its volume.
	if len(spec.Volumes) < 2 {
		t.Error("no writable volume was added for the path the app did not claim")
	}
}

// Every volume a container mounts has to exist in the pod spec, or the pod is rejected.
func TestEveryMountHasItsVolume(t *testing.T) {
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{Name: "app", Ports: []corev1.ContainerPort{{ContainerPort: 80}}}},
	}
	ApplySecurityProfile(spec, ProfileRestricted)

	declared := map[string]bool{}
	for _, v := range spec.Volumes {
		if declared[v.Name] {
			t.Errorf("volume %q declared twice", v.Name)
		}
		declared[v.Name] = true
	}
	for _, m := range spec.Containers[0].VolumeMounts {
		if !declared[m.Name] {
			t.Errorf("container mounts volume %q, which the pod does not declare", m.Name)
		}
	}
	if spec.Containers[0].SecurityContext == nil {
		t.Error("the container was left with no security context")
	}
	if spec.SecurityContext == nil {
		t.Error("the pod was left with no security context")
	}
}

// Two containers must not share one SecurityContext pointer: a later mutation of one would
// silently change the other.
func TestContainersDoNotShareASecurityContext(t *testing.T) {
	spec := &corev1.PodSpec{
		Containers: []corev1.Container{{Name: "a"}, {Name: "b"}},
	}
	ApplySecurityProfile(spec, ProfileBaseline)

	if spec.Containers[0].SecurityContext == spec.Containers[1].SecurityContext {
		t.Error("both containers point at the same SecurityContext")
	}
}

func equalSpecs(a, b *corev1.PodSpec) bool {
	return a.SecurityContext == nil && b.SecurityContext == nil &&
		len(a.Volumes) == len(b.Volumes) &&
		len(a.Containers) == len(b.Containers) &&
		a.Containers[0].SecurityContext == nil && b.Containers[0].SecurityContext == nil &&
		len(a.Containers[0].VolumeMounts) == len(b.Containers[0].VolumeMounts)
}
