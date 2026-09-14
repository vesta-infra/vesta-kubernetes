package handlers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Registry passwords live in Kubernetes Secrets, not in the VestaSecret.
//
// A VestaSecret is an ordinary namespaced object readable by anyone with `get vestasecrets`,
// and it goes into project export bundles as written. The operator migrates credentials
// created before this; the API writes new ones the right way round from the start, so a
// password never touches the CRD at all.

// RegistryPasswordKey matches the operator's constant. Duplicated rather than imported
// because the API and operator are separate modules; the test below pins them together.
const RegistryPasswordKey = "password"

// registryPasswordSecretName mirrors the operator's derivation.
func registryPasswordSecretName(vestaSecretName string) string {
	return "vesta-registry-" + vestaSecretName
}

// writeRegistryPassword stores a password and returns the reference to put on the credential.
//
// Called BEFORE the VestaSecret is created, so a failure here means no credential is created
// at all -- better than one that exists and cannot authenticate.
func (h *Handler) writeRegistryPassword(ctx context.Context, name, password string) (map[string]interface{}, error) {
	secretName := registryPasswordSecretName(name)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: vestaSystemNS,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "vesta-api",
				"kubernetes.getvesta.sh/secret":  name,
				"kubernetes.getvesta.sh/purpose": "registry-password",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{RegistryPasswordKey: []byte(password)},
	}

	_, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("storing the registry password: %w", err)
		}
		// Left over from a credential of the same name that was deleted, or a retried
		// request. Overwrite, so the stored password matches what was just submitted.
		if _, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("updating the registry password: %w", err)
		}
	}

	return map[string]interface{}{"name": secretName, "key": RegistryPasswordKey}, nil
}

// readRegistryPassword resolves a credential's password for use by this process.
//
// The reference wins when set, matching the operator. The plaintext fallback is for
// credentials the operator has not migrated yet -- it runs on its own schedule, and the API
// must keep working in the gap.
func (h *Handler) readRegistryPassword(ctx context.Context, dockerConfig map[string]interface{}) string {
	ref, _, _ := unstructuredNestedMap(dockerConfig, "passwordSecretRef")
	refName := getNestedString(ref, "name")
	if refName == "" {
		return getNestedString(dockerConfig, "password")
	}

	key := getNestedString(ref, "key")
	if key == "" {
		key = RegistryPasswordKey
	}

	secret, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).Get(ctx, refName, metav1.GetOptions{})
	if err != nil {
		// A half-finished migration: fall back so a working credential keeps working.
		return getNestedString(dockerConfig, "password")
	}
	if value, ok := secret.Data[key]; ok {
		return string(value)
	}
	return getNestedString(dockerConfig, "password")
}

// deleteRegistryPassword removes the stored password when its credential goes.
//
// The operator's copies carry an owner reference and are garbage-collected, but the API
// writes these before the VestaSecret exists, so there is nothing to own them at that point.
// Removing it here stops a deleted credential leaving a live password behind.
func (h *Handler) deleteRegistryPassword(ctx context.Context, name string) {
	_ = h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).Delete(
		ctx, registryPasswordSecretName(name), metav1.DeleteOptions{})
}
