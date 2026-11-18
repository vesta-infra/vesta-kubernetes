package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// basicAuthUser is one credential as it arrives from the UI. It exists only for the
// lifetime of the request: the password is hashed, written into a Secret, and the plaintext
// is never stored, logged, or returned.
type basicAuthUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// credentialsSecretName is the Secret backing a basicAuth middleware's credentials. It is
// derived from the middleware name so the operator can find it without the middleware
// having to record where it put it.
func credentialsSecretName(middlewareName string) string {
	return "vmw-" + middlewareName + "-auth"
}

// buildHtpasswd hashes each password with bcrypt and returns the file Traefik expects:
// one "user:hash" line each.
//
// bcrypt rather than the MD5 variant htpasswd still defaults to, because these hashes end
// up in a Secret that anyone with get on the namespace can read, and MD5 at that point is
// a formality. The cost is the same one the platform already uses for its own accounts.
func buildHtpasswd(users []basicAuthUser) (string, error) {
	lines := make([]string, 0, len(users))
	for _, user := range users {
		hashed, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		if err != nil {
			return "", fmt.Errorf("hashing the password for %q: %w", user.Username, err)
		}
		lines = append(lines, user.Username+":"+string(hashed))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n", nil
}

// validateBasicAuthUsers checks what a Secret cannot express.
func validateBasicAuthUsers(users []basicAuthUser) error {
	if len(users) == 0 {
		return fmt.Errorf("add at least one user, or point secretName at a Secret you manage yourself")
	}

	seen := map[string]bool{}
	for _, user := range users {
		name := strings.TrimSpace(user.Username)
		if name == "" {
			return fmt.Errorf("every user needs a username")
		}
		// htpasswd splits on the first colon, so a colon in a username silently truncates
		// it and grants access under a name nobody configured.
		if strings.ContainsAny(name, ":\n\r") {
			return fmt.Errorf("username %q cannot contain a colon or a line break", name)
		}
		if seen[name] {
			return fmt.Errorf("username %q is listed twice", name)
		}
		seen[name] = true

		if user.Password == "" {
			return fmt.Errorf("set a password for %q", name)
		}
		// bcrypt silently truncates beyond 72 bytes, so a longer password would appear to
		// work while only its first 72 bytes mattered.
		if len(user.Password) > 72 {
			return fmt.Errorf("the password for %q is longer than 72 bytes, which bcrypt cannot represent", name)
		}
	}
	return nil
}

// writeCredentialsSecret creates or replaces the Secret holding a middleware's htpasswd
// data. It lives in vesta-system; the operator copies it into each namespace the
// middleware is projected into.
func (h *Handler) writeCredentialsSecret(ctx context.Context, middlewareName string, users []basicAuthUser) error {
	htpasswd, err := buildHtpasswd(users)
	if err != nil {
		return err
	}

	name := credentialsSecretName(middlewareName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: vestaSystemNS,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":      "vesta-api",
				"kubernetes.getvesta.sh/middleware": middlewareName,
			},
		},
		Type: corev1.SecretTypeOpaque,
		// "users" is the key Traefik reads; it is not configurable on Traefik's side.
		Data: map[string][]byte{"users": []byte(htpasswd)},
	}

	secrets := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS)
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating credentials secret: %w", err)
		}
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating credentials secret: %w", err)
		}
	}
	return nil
}

// deleteCredentialsSecret removes the source Secret. The projected copies are the
// operator's to clean up, since it is the one that knows where they went.
func (h *Handler) deleteCredentialsSecret(ctx context.Context, middlewareName string) error {
	err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
		Delete(ctx, credentialsSecretName(middlewareName), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// existingCredentialUsers reads back the usernames already configured, so that editing a
// middleware without retyping every password keeps the accounts that were not touched.
func (h *Handler) existingCredentialUsers(ctx context.Context, middlewareName string) (map[string]string, error) {
	secret, err := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS).
		Get(ctx, credentialsSecretName(middlewareName), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}

	out := map[string]string{}
	for _, line := range strings.Split(string(secret.Data["users"]), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		username, hash, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		out[username] = hash
	}
	return out, nil
}

// mergeCredentials combines the submitted users with the hashes already stored.
//
// A user submitted with an empty password keeps the hash it already had. That is what lets
// the UI list existing accounts without ever holding their passwords -- it can show the
// usernames, and send back only the ones actually being changed.
func mergeCredentials(submitted []basicAuthUser, existing map[string]string) (lines []string, needHashing []basicAuthUser, err error) {
	for _, user := range submitted {
		name := strings.TrimSpace(user.Username)
		if user.Password == "" {
			hash, ok := existing[name]
			if !ok {
				return nil, nil, fmt.Errorf("set a password for %q", name)
			}
			lines = append(lines, name+":"+hash)
			continue
		}
		needHashing = append(needHashing, basicAuthUser{Username: name, Password: user.Password})
	}
	return lines, needHashing, nil
}

// writeCredentialsSecretMerging stores a mix of already-hashed lines and newly submitted
// passwords as one htpasswd file.
func (h *Handler) writeCredentialsSecretMerging(ctx context.Context, middlewareName string, kept []string, fresh []basicAuthUser) error {
	lines := append([]string{}, kept...)
	for _, user := range fresh {
		hashed, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hashing the password for %q: %w", user.Username, err)
		}
		lines = append(lines, user.Username+":"+string(hashed))
	}
	if len(lines) == 0 {
		return fmt.Errorf("a basicAuth middleware with no users would lock everyone out")
	}
	sort.Strings(lines)

	name := credentialsSecretName(middlewareName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: vestaSystemNS,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":      "vesta-api",
				"kubernetes.getvesta.sh/middleware": middlewareName,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"users": []byte(strings.Join(lines, "\n") + "\n")},
	}

	secrets := h.K8s.Clientset.CoreV1().Secrets(vestaSystemNS)
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating credentials secret: %w", err)
		}
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating credentials secret: %w", err)
		}
	}
	return nil
}
