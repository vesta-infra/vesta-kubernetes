package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Getting registry passwords out of the CRD.
//
// A VestaSecret is an ordinary namespaced object. Anyone with `get vestasecrets` could read
// spec.dockerConfig.password, and project export bundles carried it as written -- which
// contradicted the reasoning already applied to basicAuth and DrainSecretRef, both of which
// keep their credentials in a Kubernetes Secret.
//
// The migration below is the whole design problem. Moving a password is a two-step change
// across two objects, and getting the order wrong loses a credential that cannot be
// recovered from anywhere: clear the field before the Secret is durably written and the
// value is gone, with the only symptom an ImagePullBackOff nobody can fix without knowing
// the original password.
//
// So it is strictly: write the Secret, confirm it reads back, and only then clear the
// plaintext. Every failure mode leaves the plaintext in place and retries on the next pass.
// The worst outcome is a duplicate that does no harm.

// RegistryPasswordKey is the key inside the generated Secret.
const RegistryPasswordKey = "password"

// registryPasswordSecretName is where a credential's password lives once migrated. Derived
// from the VestaSecret's own name so the mapping needs no lookup table.
func registryPasswordSecretName(vestaSecretName string) string {
	return "vesta-registry-" + vestaSecretName
}

// ResolveRegistryPassword returns the password for a docker credential.
//
// The reference wins when set. That precedence is what makes the migration one-way: once the
// ref exists, the plaintext field is dead weight rather than a second source of truth that
// has to be kept consistent.
func (r *VestaSecretReconciler) ResolveRegistryPassword(
	ctx context.Context, namespace string, dc *vestav1alpha1.DockerSecretConfig,
) (string, error) {

	if dc == nil {
		return "", nil
	}
	if dc.PasswordSecretRef == nil || dc.PasswordSecretRef.Name == "" {
		return dc.Password, nil
	}

	key := dc.PasswordSecretRef.Key
	if key == "" {
		key = RegistryPasswordKey
	}

	var secret corev1.Secret
	// Uncached: a credential read moments after its Secret was written would otherwise miss
	// it and fall back to a plaintext field the migration has just emptied.
	err := r.reader().Get(ctx, client.ObjectKey{Namespace: namespace, Name: dc.PasswordSecretRef.Name}, &secret)
	if err != nil {
		if apierrors.IsNotFound(err) && dc.Password != "" {
			// A ref pointing at nothing, with the plaintext still present: this is a
			// half-finished migration, and falling back keeps image pulls working while the
			// next pass repairs it. Refusing here would break a credential that is fine.
			return dc.Password, nil
		}
		return "", fmt.Errorf("reading registry password from Secret %q: %w", dc.PasswordSecretRef.Name, err)
	}

	value, ok := secret.Data[key]
	if !ok {
		if dc.Password != "" {
			return dc.Password, nil
		}
		return "", fmt.Errorf("Secret %q has no key %q", dc.PasswordSecretRef.Name, key)
	}
	return string(value), nil
}

// MigrateRegistryPassword moves a plaintext password into a Secret.
//
// Returns whether anything changed. Does nothing for a credential that is already migrated,
// and nothing for one with no password to move.
func (r *VestaSecretReconciler) MigrateRegistryPassword(
	ctx context.Context, vs *vestav1alpha1.VestaSecret,
) (bool, error) {

	dc := vs.Spec.DockerConfig
	if !NeedsPasswordMigration(dc) {
		return false, nil
	}

	name := registryPasswordSecretName(vs.Name)

	// Step one: write the Secret. Owned by the VestaSecret, so deleting the credential takes
	// the password with it rather than leaving an orphan holding a live secret.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: vs.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "vesta-operator",
				"kubernetes.getvesta.sh/secret":  vs.Name,
				"kubernetes.getvesta.sh/purpose": "registry-password",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{RegistryPasswordKey: []byte(dc.Password)},
	}
	if err := controllerutil.SetControllerReference(vs, secret, r.Scheme); err != nil {
		return false, err
	}

	if err := r.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return false, fmt.Errorf("creating password Secret: %w", err)
		}
		// Already there from an interrupted pass. Overwrite it with the value we are about
		// to clear, so the two cannot disagree.
		var existing corev1.Secret
		if err := r.reader().Get(ctx, client.ObjectKey{Namespace: vs.Namespace, Name: name}, &existing); err != nil {
			return false, err
		}
		existing.Data = map[string][]byte{RegistryPasswordKey: []byte(dc.Password)}
		if err := r.Update(ctx, &existing); err != nil {
			return false, fmt.Errorf("updating password Secret: %w", err)
		}
	}

	// Step two: confirm it reads back before anything is cleared. A create that the API
	// server accepted but that is not readable would otherwise be enough to destroy the
	// only copy of the password.
	var written corev1.Secret
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: vs.Namespace, Name: name}, &written); err != nil {
		return false, fmt.Errorf("verifying password Secret: %w", err)
	}
	if string(written.Data[RegistryPasswordKey]) != dc.Password {
		return false, fmt.Errorf("password Secret %q does not hold the expected value; not clearing the original", name)
	}

	// Step three, and only now: point at the Secret and drop the plaintext.
	patched := vs.DeepCopy()
	patched.Spec.DockerConfig.PasswordSecretRef = &vestav1alpha1.DrainSecretRef{
		Name: name,
		Key:  RegistryPasswordKey,
	}
	patched.Spec.DockerConfig.Password = ""

	if err := r.Patch(ctx, patched, client.MergeFrom(vs)); err != nil {
		// The Secret is written and verified, so the credential is safe; the plaintext is
		// simply still there and the next pass will try again.
		return false, fmt.Errorf("clearing plaintext password: %w", err)
	}

	// Hand the caller the object as it now stands, so nothing downstream reads a password
	// field this call just emptied, and nothing re-reads through a cache that has not caught
	// up with the patch.
	patched.DeepCopyInto(vs)

	return true, nil
}

// NeedsPasswordMigration reports whether a credential still carries a plaintext password
// that has not been moved.
//
// Pure, so the condition can be tested without a cluster -- it is the guard on a destructive
// operation, and every case where it wrongly returns true is a case where a password gets
// cleared.
func NeedsPasswordMigration(dc *vestav1alpha1.DockerSecretConfig) bool {
	if dc == nil {
		return false
	}
	if dc.Password == "" {
		// Nothing to move. Also the already-migrated case.
		return false
	}
	if dc.PasswordSecretRef != nil && dc.PasswordSecretRef.Name != "" {
		// Already points somewhere. The plaintext left behind is cleaned up by the patch
		// that set the ref; if it is still here, a previous pass failed at the last step and
		// clearing it now would be correct -- but only once the Secret is confirmed, which
		// is the caller's job, not this predicate's.
		return true
	}
	return true
}
