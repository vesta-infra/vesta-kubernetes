package controllers

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// Pod and container hardening.
//
// The whole design turns on one constraint: this cannot change the behaviour of an app that
// is already running. A container that runs as root, or writes to its own filesystem, or
// binds port 80, is not doing anything wrong -- it is doing what its image was built to do --
// and a platform upgrade that silently starts killing those is a far worse outcome than a
// platform that ships unhardened by default.
//
// So the default profile is "legacy", which sets nothing at all, and hardening is something
// an operator opts into per instance or per app. The profiles below are ordered so that each
// step is one somebody can actually take:
//
//	legacy      nothing. What every existing app gets, and what it keeps on upgrade.
//	baseline    the restrictions almost no app notices: no privilege escalation, no
//	            capabilities, the default seccomp filter. Does NOT force a non-root user or
//	            a read-only filesystem, because those are the two that break real images.
//	restricted  baseline plus non-root and a read-only root filesystem.
//
// The gap between baseline and restricted is deliberate. Baseline is safe to turn on across
// an instance; restricted is not, and pretending otherwise would mean the setting gets turned
// on once, breaks something, and is turned off forever.

const (
	ProfileLegacy     = "legacy"
	ProfileBaseline   = "baseline"
	ProfileRestricted = "restricted"
)

// ResolveSecurityProfile picks the profile for one app.
//
// An app's own setting wins, so a single image that cannot run hardened does not force the
// whole instance back down to legacy.
func ResolveSecurityProfile(platform, app string) string {
	for _, candidate := range []string{app, platform} {
		switch candidate {
		case ProfileBaseline, ProfileRestricted, ProfileLegacy:
			return candidate
		}
	}
	return ProfileLegacy
}

// Hardened reports whether a profile does anything, so callers can skip the rest of the work.
func Hardened(profile string) bool {
	return profile == ProfileBaseline || profile == ProfileRestricted
}

// restrictedUID is the identity a restricted container runs as.
//
// A fixed non-root UID rather than merely runAsNonRoot: on its own, runAsNonRoot only makes
// the kubelet REFUSE to start an image whose USER is root or unset, which turns a hardening
// setting into a CreateContainerConfigError. Naming a UID means such an image runs instead.
const restrictedUID int64 = 1000

// SecurityContexts renders the pod- and container-level contexts for a profile.
//
// ports is what the container listens on, and it is here for one reason: dropping all
// capabilities takes away NET_BIND_SERVICE, and a container that binds a port below 1024
// then fails to start. That is a silent, confusing break -- the image is fine, the config
// looks right, and the pod simply crashes -- so the capability is granted back when, and
// only when, the app actually needs it.
func SecurityContexts(profile string, ports []int32) (*corev1.PodSecurityContext, *corev1.SecurityContext) {
	if !Hardened(profile) {
		return nil, nil
	}

	caps := &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	if needsPrivilegedPort(ports) {
		caps.Add = []corev1.Capability{"NET_BIND_SERVICE"}
	}

	container := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Privileged:               ptr.To(false),
		Capabilities:             caps,
	}

	pod := &corev1.PodSecurityContext{
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}

	if profile == ProfileRestricted {
		container.RunAsNonRoot = ptr.To(true)
		container.RunAsUser = ptr.To(restrictedUID)
		container.ReadOnlyRootFilesystem = ptr.To(true)

		pod.RunAsNonRoot = ptr.To(true)
		pod.RunAsUser = ptr.To(restrictedUID)
		// fsGroup so a mounted volume is writable by the non-root user. Without it a
		// restricted app with a PVC starts and then cannot write to it, which looks like an
		// application bug rather than a platform setting.
		pod.FSGroup = ptr.To(restrictedUID)
	}

	return pod, container
}

func needsPrivilegedPort(ports []int32) bool {
	for _, p := range ports {
		if p > 0 && p < 1024 {
			return true
		}
	}
	return false
}

// writableMounts are the paths a read-only root filesystem has to punch holes for.
//
// Without these, readOnlyRootFilesystem is not a hardening setting, it is an outage: almost
// every runtime writes to /tmp, and many write a pid or socket into /var/run before serving
// a single request.
var writableMounts = []struct{ name, path string }{
	{"vesta-tmp", "/tmp"},
	{"vesta-run", "/var/run"},
}

// WritableVolumes returns the emptyDirs a profile needs, and the mounts that go with them.
//
// Returns nothing for any profile that does not set a read-only root filesystem, so an app
// on baseline is not given volumes it has no use for.
func WritableVolumes(profile string) ([]corev1.Volume, []corev1.VolumeMount) {
	if profile != ProfileRestricted {
		return nil, nil
	}

	volumes := make([]corev1.Volume, 0, len(writableMounts))
	mounts := make([]corev1.VolumeMount, 0, len(writableMounts))
	for _, m := range writableMounts {
		volumes = append(volumes, corev1.Volume{
			Name:         m.name,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: m.name, MountPath: m.path})
	}
	return volumes, mounts
}

// ApplySecurityProfile stamps a profile onto a pod spec.
//
// Mounts are appended only where the app has not already mounted that path itself: an app
// with its own volume at /tmp means it, and replacing it with an emptyDir would quietly
// discard whatever it was.
func ApplySecurityProfile(spec *corev1.PodSpec, profile string) {
	if !Hardened(profile) || spec == nil || len(spec.Containers) == 0 {
		return
	}

	var ports []int32
	for _, c := range spec.Containers {
		for _, p := range c.Ports {
			ports = append(ports, p.ContainerPort)
		}
	}

	podSC, containerSC := SecurityContexts(profile, ports)
	spec.SecurityContext = podSC

	volumes, mounts := WritableVolumes(profile)

	taken := map[string]bool{}
	for _, c := range spec.Containers {
		for _, m := range c.VolumeMounts {
			taken[m.MountPath] = true
		}
	}

	var added []corev1.Volume
	for i := range spec.Containers {
		spec.Containers[i].SecurityContext = containerSC.DeepCopy()
		for j, m := range mounts {
			if taken[m.MountPath] {
				continue
			}
			spec.Containers[i].VolumeMounts = append(spec.Containers[i].VolumeMounts, m)
			if i == 0 {
				added = append(added, volumes[j])
			}
		}
	}
	spec.Volumes = append(spec.Volumes, added...)
}
