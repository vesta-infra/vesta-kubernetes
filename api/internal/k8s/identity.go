package k8s

import (
	"context"
	"crypto/ecdh"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubernetes.getvesta.sh/api/internal/bundle"
)

const (
	// identitySecretName holds this installation's long-lived X25519 keypair, the thing
	// that makes "only this instance can open that bundle" true. Losing it means every
	// bundle sealed for this instance becomes permanently unreadable, so it is stored in
	// etcd next to every other Vesta secret rather than in the API's own database.
	identitySecretName = "vesta-instance-identity"
	identityPrivateKey = "privateKey"
)

// InstanceIdentity returns this installation's keypair, creating it on first call.
//
// Two API replicas racing on a cold start both generate a key and both try to create the
// Secret; the loser gets AlreadyExists and re-reads the winner's. Without that re-read
// the two replicas would disagree about the instance's identity and bundles would open
// only when they happened to hit the right pod.
func (c *Client) InstanceIdentity(ctx context.Context, namespace string) (*ecdh.PrivateKey, error) {
	secrets := c.Clientset.CoreV1().Secrets(namespace)

	existing, err := secrets.Get(ctx, identitySecretName, metav1.GetOptions{})
	if err == nil {
		return parseIdentitySecret(existing)
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("reading instance identity: %w", err)
	}

	priv, err := bundle.GenerateIdentity()
	if err != nil {
		return nil, err
	}

	created, err := secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      identitySecretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vesta",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{identityPrivateKey: priv.Bytes()},
	}, metav1.CreateOptions{})
	if err == nil {
		return parseIdentitySecret(created)
	}
	if !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("storing instance identity: %w", err)
	}

	winner, err := secrets.Get(ctx, identitySecretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading instance identity after create race: %w", err)
	}
	return parseIdentitySecret(winner)
}

func parseIdentitySecret(s *corev1.Secret) (*ecdh.PrivateKey, error) {
	raw, ok := s.Data[identityPrivateKey]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("instance identity secret %q has no %q key", identitySecretName, identityPrivateKey)
	}
	priv, err := bundle.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("instance identity secret %q: %w", identitySecretName, err)
	}
	return priv, nil
}

// EnsureNamespace creates a namespace if it is not already there.
//
// Namespaces are normally the operator's job, but an import writes ConfigMaps and
// VestaSecrets into {project}-{env} before any reconcile has run, so it has to create
// them itself or every one of those writes fails NotFound.
func (c *Client) EnsureNamespace(ctx context.Context, name string) error {
	namespaces := c.Clientset.CoreV1().Namespaces()
	if _, err := namespaces.Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("reading namespace %q: %w", name, err)
	}

	_, err := namespaces.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "vesta"},
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating namespace %q: %w", name, err)
	}
	return nil
}
