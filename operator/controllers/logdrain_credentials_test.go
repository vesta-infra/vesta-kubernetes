package controllers

import (
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

// Editing a drain's password changed nothing on the running collector.
//
// Credentials reach Fluent Bit as env vars built from a SecretKeyRef, which names a Secret
// and a key. Rewriting the password behind that reference leaves the env slice identical,
// and the rendered configuration is identical too because it holds ${VAR} rather than the
// value. Both comparisons in rollCollector therefore said "unchanged", no roll happened, and
// the pods went on presenting the credential they had read at startup.
//
// The failure is a quiet one: the drain keeps running, the operator keeps reporting on it,
// and the destination keeps returning 401. The only fix available to a user was to delete
// the drain and create it again, which restarts the collector as a side effect.
func TestCredentialDigestChangesWithTheValue(t *testing.T) {
	before := credentialDigest(map[string][]byte{
		"VESTA_DRAIN_ACME_USER":     []byte("dev@example.com"),
		"VESTA_DRAIN_ACME_PASSWORD": []byte("old-password"),
	})
	after := credentialDigest(map[string][]byte{
		"VESTA_DRAIN_ACME_USER":     []byte("dev@example.com"),
		"VESTA_DRAIN_ACME_PASSWORD": []byte("new-password"),
	})

	if before == after {
		t.Fatal("the same digest for two different passwords, so a password change is " +
			"invisible to the DaemonSet comparison and the collector is never restarted")
	}
}

// The negative control: identical credentials must hash identically, whatever order the map
// yields them in. A digest that moved on its own would roll the collector on every reconcile,
// which costs more than the bug it fixes -- every node's Fluent Bit restarting in a loop,
// dropping whatever was buffered each time.
func TestCredentialDigestIsStableForTheSameValues(t *testing.T) {
	values := func() map[string][]byte {
		return map[string][]byte{
			"VESTA_DRAIN_ACME_USER":     []byte("dev@example.com"),
			"VESTA_DRAIN_ACME_PASSWORD": []byte("s3cret"),
			"VESTA_DRAIN_OTHER_TOKEN":   []byte("abc"),
		}
	}
	for i := 0; i < 32; i++ {
		if credentialDigest(values()) != credentialDigest(values()) {
			t.Fatal("digest is not stable across calls, so the collector would roll on every reconcile")
		}
	}
}

// Renaming a drain moves the value to a different env var. The collector has to restart for
// that too, since the old variable stops being set and the config now references a new one.
func TestCredentialDigestCoversTheVariableName(t *testing.T) {
	a := credentialDigest(map[string][]byte{"VESTA_DRAIN_ONE_TOKEN": []byte("x")})
	b := credentialDigest(map[string][]byte{"VESTA_DRAIN_TWO_TOKEN": []byte("x")})
	if a == b {
		t.Fatal("same digest under two variable names, so renaming a drain does not restart the collector")
	}
}

// Length-prefixing, checked directly: concatenating name and value without lengths lets
// different credential sets collide, and a collision here means a password change that never
// ships.
func TestCredentialDigestDoesNotCollideOnSplit(t *testing.T) {
	a := credentialDigest(map[string][]byte{"VESTA_DRAIN_A_USER": []byte("ab")})
	b := credentialDigest(map[string][]byte{"VESTA_DRAIN_A_USERa": []byte("b")})
	if a == b {
		t.Fatal("two different credential sets hash the same, so one can mask a change to the other")
	}
}

// An empty digest removes the annotation rather than writing "". A collector with no
// credentials must compare equal to a freshly built one, or the operator-owned path sees a
// difference on every pass and restarts the DaemonSet forever.
func TestNoCredentialsLeavesNoAnnotation(t *testing.T) {
	ds := &appsv1.DaemonSet{}
	stampCredentials(ds, "abc")
	if ds.Spec.Template.Annotations[credentialChecksumAnnotation] != "abc" {
		t.Fatal("stampCredentials did not record the digest")
	}

	stampCredentials(ds, "")
	if _, present := ds.Spec.Template.Annotations[credentialChecksumAnnotation]; present {
		t.Fatal("an empty digest left the annotation behind, so a collector with no credentials " +
			"never compares equal to a freshly built one and rolls on every reconcile")
	}
}

// Both DaemonSet paths have to consult the digest, not just one.
//
// A chart-rendered collector is compared field by field against `unchanged`; an
// operator-owned one is rebuilt and compared with DeepEqual. Fixing only the path that was
// being exercised at the time leaves the other one carrying exactly the original bug, on a
// cluster that installed the collector the other way.
func TestBothCollectorPathsSeeCredentialChanges(t *testing.T) {
	raw, err := os.ReadFile("vestalogdrain_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	if !strings.Contains(src, "ds.Spec.Template.Annotations[credentialChecksumAnnotation] == digest") {
		t.Fatal("the chart-rendered path does not compare the credential digest, so editing a " +
			"password does not restart a collector installed by Helm")
	}

	// The operator-owned path compares whole specs, so it only needs the digest stamped onto
	// the object it builds -- but if that stamp is missing the DeepEqual can never see a
	// credential change either.
	if strings.Count(src, "stampCredentials(") < 4 {
		t.Fatal("not every collector path stamps the credential digest; the one that does not " +
			"reproduces the original bug")
	}
}

// The digest is inert without a watch on the Secrets it hashes.
//
// The API rewrites the VestaLogDrain on every edit, which looks like it would be enough. It
// is not: a credential lives in the Secret, and the spec holds only the Secret's name and
// key, so a credential-only edit writes the object back unchanged. Kubernetes does not move
// resourceVersion for a no-op update and emits no watch event, so nothing reconciles and the
// digest is never recomputed.
//
// Cheap to add, easy to drop in a refactor, and its absence restores the original bug in a
// form that looks fixed -- the comparison is right there in the code, just never reached.
func TestCredentialSecretsAreWatched(t *testing.T) {
	raw, err := os.ReadFile("vestalogdrain_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	setup := src[strings.Index(src, "func (r *VestaLogDrainReconciler) SetupWithManager"):]
	if !strings.Contains(setup, "&corev1.Secret{}") {
		t.Fatal("the drain controller does not watch credential Secrets, so editing a password " +
			"alone reconciles nothing and the collector keeps the credential it started with")
	}
}
