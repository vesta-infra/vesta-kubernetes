package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"kubernetes.getvesta.sh/api/internal/crypto"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/mfa"
	"kubernetes.getvesta.sh/api/internal/middleware"
	"kubernetes.getvesta.sh/api/internal/models"
)

// webauthnCeremonyTTL bounds how long a challenge stays claimable. Long enough to pick a
// security key out of a drawer, short enough that an abandoned challenge is not left
// lying around.
const webauthnCeremonyTTL = 5 * time.Minute

var (
	mfaCipherOnce sync.Once
	mfaCipher     *crypto.Cipher
)

// totpCipher returns the cipher protecting TOTP secrets at rest, or nil when no key is
// configured.
//
// A nil cipher disables TOTP specifically -- see the crypto package's own doc: it is
// "this feature is unavailable", not a fatal error. Passkeys need no key and must keep
// working on an install that never set one, so this cannot be a startup failure.
func totpCipher() *crypto.Cipher {
	mfaCipherOnce.Do(func() {
		c, err := crypto.NewCipherFromBase64(os.Getenv("VESTA_ENCRYPTION_KEY"))
		if err != nil {
			if !errors.Is(err, crypto.ErrNoKey) {
				log.Printf("VESTA_ENCRYPTION_KEY is set but unusable, authenticator-app 2FA is disabled: %v", err)
			} else {
				log.Println("VESTA_ENCRYPTION_KEY is unset: authenticator-app 2FA is unavailable (passkeys still work)")
			}
			return
		}
		mfaCipher = c
	})
	return mfaCipher
}

// mfaPolicy reads the admin-configured policy.
func (h *Handler) mfaPolicy(ctx context.Context) mfa.Policy {
	return mfa.Policy{RequireAdmin: h.DB.GetBoolSetting(ctx, db.SettingMFARequireAdmin, false)}
}

// ---------- WebAuthn plumbing ----------

// webauthnUser adapts a Vesta user to the library's User interface.
type webauthnUser struct {
	user  *db.User
	creds []webauthn.Credential
}

// WebAuthnID must be stable and opaque. The user's UUID is both, and using it means a
// credential stays bound to the account even if the username or email changes.
func (u *webauthnUser) WebAuthnID() []byte { return []byte(u.user.ID) }

func (u *webauthnUser) WebAuthnName() string { return u.user.Username }

func (u *webauthnUser) WebAuthnDisplayName() string {
	if u.user.DisplayName != "" {
		return u.user.DisplayName
	}
	return u.user.Username
}

func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (h *Handler) loadWebAuthnUser(ctx context.Context, userID string) (*webauthnUser, error) {
	user, err := h.DB.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	stored, err := h.DB.ListWebAuthnCredentials(ctx, userID)
	if err != nil {
		return nil, err
	}

	creds := make([]webauthn.Credential, 0, len(stored))
	for _, s := range stored {
		transports := make([]protocol.AuthenticatorTransport, 0, len(s.Transports))
		for _, t := range s.Transports {
			transports = append(transports, protocol.AuthenticatorTransport(t))
		}
		creds = append(creds, webauthn.Credential{
			ID:              s.CredentialID,
			PublicKey:       s.PublicKey,
			AttestationType: s.AttestationType,
			Transport:       transports,
			Flags: webauthn.CredentialFlags{
				UserPresent:    true,
				UserVerified:   s.UserVerified,
				BackupEligible: s.BackupEligible,
				BackupState:    s.BackupState,
			},
			Authenticator: webauthn.Authenticator{AAGUID: s.AAGUID, SignCount: s.SignCount},
		})
	}
	return &webauthnUser{user: user, creds: creds}, nil
}

// newWebAuthn builds a relying party for this request.
//
// It is per-request rather than per-process because the RP ID has to match the origin the
// browser is actually on, and one Vesta can be reached on more than one hostname. See
// mfa.ResolveRelyingParty for why the Origin header beats the API's own Host.
func newWebAuthn(c *gin.Context) (*webauthn.WebAuthn, mfa.RelyingParty, error) {
	rp, err := mfa.ResolveRelyingParty(
		c.GetHeader("Origin"),
		c.Request.Host,
		c.Request.TLS != nil,
		allowedWebAuthnOrigins(),
	)
	if err != nil {
		return nil, rp, err
	}

	w, err := webauthn.New(&webauthn.Config{
		RPID:          rp.ID,
		RPDisplayName: "Vesta",
		RPOrigins:     []string{rp.Origin},
	})
	return w, rp, err
}

// allowedWebAuthnOrigins reuses the origin allowlist the WebSocket layer already reads,
// so an operator configures trusted browser origins in exactly one place.
func allowedWebAuthnOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("VESTA_ALLOWED_ORIGINS"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// replayBody lets the library's request-based finish helpers read a body this handler has
// already consumed, and re-reads it as the *http.Request they expect.
func replayBody(c *gin.Context) (*http.Request, error) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return c.Request, nil
}

// ---------- Status ----------

// GetMFAStatus describes the caller's factors and what the policy demands of them.
func (h *Handler) GetMFAStatus(c *gin.Context) {
	userID := c.GetString("userId")

	user, err := h.DB.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	enrollments, err := h.DB.ListEnrollments(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	remaining, _ := h.DB.CountUnusedBackupCodes(c.Request.Context(), userID)
	policy := h.mfaPolicy(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"enrollments":        enrollments,
		"methods":            mfa.Methods(enrollments),
		"enabled":            len(enrollments) > 0,
		"required":           mfa.RequiredFor(user.Role, policy),
		"backupCodesLeft":    remaining,
		"totpAvailable":      totpCipher() != nil,
		"requireAdminPolicy": policy.RequireAdmin,
	})
}

// ---------- TOTP enrollment ----------

// EnrollTOTP issues a fresh secret and the otpauth URL for it. The factor does not count
// until ConfirmTOTP proves the user's authenticator holds the same secret.
func (h *Handler) EnrollTOTP(c *gin.Context) {
	cipher := totpCipher()
	if cipher == nil {
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{
			Code:    503,
			Message: "authenticator-app 2FA is unavailable: this instance has no VESTA_ENCRYPTION_KEY configured",
		})
		return
	}

	userID := c.GetString("userId")
	user, err := h.DB.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	// Gated at the start of the ceremony rather than at confirm: the user finds out
	// before scanning a QR code, and ConfirmTOTP can only act on the pending row this
	// call creates.
	if !h.requireReauthForNewFactor(c, userID) {
		return
	}

	secret, err := mfa.GenerateSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// The user id is bound in as AAD, so a ciphertext lifted from one row cannot be
	// replanted in another and still decrypt.
	ciphertext, err := cipher.Encrypt([]byte(secret), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	if err := h.DB.UpsertPendingTOTP(c.Request.Context(), userID, ciphertext, time.Now().Add(mfaEnrollTTL)); err != nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: err.Error()})
		return
	}

	account := user.Email
	if account == "" {
		account = user.Username
	}

	otpauthURL := mfa.OTPAuthURL("Vesta", account, secret)
	// A failed render is not fatal: the secret is also shown for manual entry, which is
	// the fallback every authenticator app supports.
	qr, err := mfa.QRDataURI(otpauthURL)
	if err != nil {
		log.Printf("mfa: rendering QR code: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"secret":     secret,
		"otpauthUrl": otpauthURL,
		"qrDataUri":  qr,
		"digits":     mfa.TOTPDigits,
		"period":     mfa.TOTPPeriod,
		"expiresAt":  time.Now().Add(mfaEnrollTTL).Format(time.RFC3339),
	})
}

// ConfirmTOTP completes enrollment and returns the recovery codes, which are shown once.
func (h *Handler) ConfirmTOTP(c *gin.Context) {
	cipher := totpCipher()
	if cipher == nil {
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{Code: 503, Message: "authenticator-app 2FA is unavailable"})
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	enrollment, err := h.DB.GetTOTP(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "start enrollment first"})
		return
	}
	if enrollment.Confirmed() {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "an authenticator app is already enrolled"})
		return
	}
	if enrollment.PendingExpiresAt != nil && time.Now().After(*enrollment.PendingExpiresAt) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "enrollment expired, start again"})
		return
	}

	secret, err := cipher.Decrypt(enrollment.SecretCiphertext, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "stored secret could not be read"})
		return
	}

	counter, err := mfa.VerifyTOTP(string(secret), req.Code, time.Now(), 0)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "that code is not correct"})
		return
	}

	if err := h.DB.ConfirmTOTP(c.Request.Context(), userID, counter); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	codes, err := h.issueBackupCodes(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"confirmed": true, "backupCodes": codes})
	h.auditLog(c, "mfa_enroll_totp", "user", userID, userID, "", "", nil)
}

// DisableTOTP removes the authenticator factor, subject to the anti-lockout rule.
func (h *Handler) DisableTOTP(c *gin.Context) {
	userID := c.GetString("userId")

	// Anti-lockout first, then the grant: a removal that policy forbids should not cost
	// the user a re-authentication they would have to repeat.
	if err := h.guardLastFactor(c, userID, mfa.MethodTOTP, ""); err != nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: err.Error()})
		return
	}
	if !h.requireReauth(c, userID) {
		return
	}
	if err := h.DB.DeleteTOTP(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	h.clearBackupCodesIfNoFactors(c.Request.Context(), userID)
	c.Status(http.StatusNoContent)
	h.auditLog(c, "mfa_disable_totp", "user", userID, userID, "", "", nil)
}

// ---------- WebAuthn registration ----------

func (h *Handler) BeginWebAuthnRegistration(c *gin.Context) {
	w, rp, err := newWebAuthn(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	user, err := h.loadWebAuthnUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	// FinishWebAuthnRegistration can only consume a session this call created, so gating
	// here covers the whole ceremony and the prompt appears before the user is asked to
	// touch anything.
	if !h.requireReauthForNewFactor(c, userID) {
		return
	}

	// Excluding existing credentials makes the authenticator refuse to register itself
	// twice, so a user cannot end up with two entries for one key.
	exclusions := make([]protocol.CredentialDescriptor, 0, len(user.creds))
	for _, cred := range user.creds {
		exclusions = append(exclusions, cred.Descriptor())
	}

	creation, session, err := w.BeginRegistration(user,
		webauthn.WithExclusions(exclusions),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		}),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	encoded, err := json.Marshal(session)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	sessionID, err := h.DB.CreateWebAuthnSession(c.Request.Context(), userID, "register", encoded, time.Now().Add(webauthnCeremonyTTL))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionId": sessionID, "publicKey": creation.Response, "rpId": rp.ID})
}

func (h *Handler) FinishWebAuthnRegistration(c *gin.Context) {
	sessionID := c.Query("sessionId")
	name := strings.TrimSpace(c.Query("name"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "sessionId is required"})
		return
	}

	w, rp, err := newWebAuthn(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	raw, err := h.DB.TakeWebAuthnSession(c.Request.Context(), sessionID, userID, "register")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "that registration attempt has expired, start again"})
		return
	}

	var session webauthn.SessionData
	if err := json.Unmarshal(raw, &session); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	user, err := h.loadWebAuthnUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	req, err := replayBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	credential, err := w.FinishRegistration(user, session, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: describeWebAuthnError(err)})
		return
	}

	if name == "" {
		name = "Passkey"
	}
	transports := make([]string, 0, len(credential.Transport))
	for _, t := range credential.Transport {
		transports = append(transports, string(t))
	}

	id, err := h.DB.InsertWebAuthnCredential(c.Request.Context(), &db.WebAuthnCredential{
		UserID:          userID,
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		Transports:      transports,
		AttestationType: credential.AttestationType,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		UserVerified:    credential.Flags.UserVerified,
		RPID:            rp.ID,
		Name:            name,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// First factor of any kind earns a set of recovery codes; a later one must not
	// silently invalidate codes the user has already written down.
	var codes []string
	if existing, _ := h.DB.CountUnusedBackupCodes(c.Request.Context(), userID); existing == 0 {
		codes, _ = h.issueBackupCodes(c.Request.Context(), userID)
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "name": name, "backupCodes": codes})
	h.auditLog(c, "mfa_enroll_webauthn", "user", userID, userID, "", "", map[string]interface{}{"name": name})
}

// ListWebAuthnCredentials returns the caller's registered passkeys.
func (h *Handler) ListWebAuthnCredentials(c *gin.Context) {
	creds, err := h.DB.ListWebAuthnCredentials(c.Request.Context(), c.GetString("userId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	items := make([]map[string]interface{}, 0, len(creds))
	for _, cred := range creds {
		items = append(items, map[string]interface{}{
			"id": cred.ID, "name": cred.Name, "createdAt": cred.CreatedAt,
			"lastUsedAt": cred.LastUsedAt, "transports": cred.Transports,
		})
	}
	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) DeleteWebAuthnCredential(c *gin.Context) {
	userID := c.GetString("userId")
	id := c.Param("id")

	if err := h.guardLastFactor(c, userID, mfa.MethodWebAuthn, id); err != nil {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: err.Error()})
		return
	}
	if !h.requireReauth(c, userID) {
		return
	}
	if err := h.DB.DeleteWebAuthnCredential(c.Request.Context(), userID, id); err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "passkey not found"})
		return
	}
	h.clearBackupCodesIfNoFactors(c.Request.Context(), userID)
	c.Status(http.StatusNoContent)
	h.auditLog(c, "mfa_remove_webauthn", "user", userID, userID, "", "", map[string]interface{}{"credential": id})
}

func (h *Handler) RenameWebAuthnCredential(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	if err := h.DB.RenameWebAuthnCredential(c.Request.Context(), c.GetString("userId"), c.Param("id"), req.Name); err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "passkey not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- Verification (the challenge exchange) ----------

// VerifyMFA exchanges a TOTP or backup code for a full session.
func (h *Handler) VerifyMFA(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	ctx := c.Request.Context()

	user, err := h.DB.GetUserByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "invalid code"})
		return
	}

	if wait, locked := h.lockedOut(ctx, userID); locked {
		c.JSON(http.StatusTooManyRequests, models.ErrorResponse{
			Code:    429,
			Message: fmt.Sprintf("too many failed attempts, try again in %s", wait.Round(time.Second)),
		})
		return
	}

	method, ok := h.checkCode(ctx, userID, req.Code)
	if !ok {
		h.recordMFAFailure(c, userID)
		// Deliberately identical to "no factor enrolled": the response must not say
		// which part was wrong, or whether this account has 2FA at all.
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "invalid code"})
		return
	}

	h.clearMFAFailures(ctx, userID)
	h.issueSessionAfterMFA(c, user, string(method))
}

// checkCode tries the submitted string as whichever factor its shape suggests.
//
// Backup codes and TOTP codes cannot be confused: mfa.ValidBackupCodeFormat tells them
// apart, so the client never has to declare which one it is sending.
func (h *Handler) checkCode(ctx context.Context, userID, code string) (mfa.Method, bool) {
	if mfa.ValidBackupCodeFormat(code) {
		used, err := h.DB.ConsumeBackupCode(ctx, userID, code)
		if err == nil && used {
			return mfa.MethodBackupCode, true
		}
		return "", false
	}

	cipher := totpCipher()
	if cipher == nil {
		return "", false
	}
	enrollment, err := h.DB.GetTOTP(ctx, userID)
	if err != nil || !enrollment.Confirmed() {
		return "", false
	}
	secret, err := cipher.Decrypt(enrollment.SecretCiphertext, userID)
	if err != nil {
		return "", false
	}

	counter, err := mfa.VerifyTOTP(string(secret), code, time.Now(), enrollment.LastCounter)
	if err != nil {
		if errors.Is(err, mfa.ErrCodeReplayed) {
			// Worth its own audit line -- a replay means someone had a valid code they
			// should not have -- but it reaches the client as a plain failure.
			log.Printf("mfa: replayed TOTP code for user %s", userID)
		}
		return "", false
	}
	if err := h.DB.AdvanceTOTPCounter(ctx, userID, counter); err != nil {
		return "", false
	}
	return mfa.MethodTOTP, true
}

func (h *Handler) BeginWebAuthnAuthentication(c *gin.Context) {
	w, _, err := newWebAuthn(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	user, err := h.loadWebAuthnUser(c.Request.Context(), userID)
	if err != nil || len(user.creds) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "no passkey is registered"})
		return
	}

	assertion, session, err := w.BeginLogin(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	encoded, _ := json.Marshal(session)
	sessionID, err := h.DB.CreateWebAuthnSession(c.Request.Context(), userID, "authenticate", encoded, time.Now().Add(webauthnCeremonyTTL))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionId": sessionID, "publicKey": assertion.Response})
}

func (h *Handler) FinishWebAuthnAuthentication(c *gin.Context) {
	sessionID := c.Query("sessionId")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "sessionId is required"})
		return
	}

	w, _, err := newWebAuthn(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	ctx := c.Request.Context()

	if wait, locked := h.lockedOut(ctx, userID); locked {
		c.JSON(http.StatusTooManyRequests, models.ErrorResponse{
			Code:    429,
			Message: fmt.Sprintf("too many failed attempts, try again in %s", wait.Round(time.Second)),
		})
		return
	}

	raw, err := h.DB.TakeWebAuthnSession(ctx, sessionID, userID, "authenticate")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "that attempt has expired, start again"})
		return
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(raw, &session); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	user, err := h.loadWebAuthnUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "invalid credential"})
		return
	}

	req, err := replayBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	credential, err := w.FinishLogin(user, session, req)
	if err != nil {
		h.recordMFAFailure(c, userID)
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "invalid credential"})
		return
	}

	// A clone of an authenticator shows up as a sign count that failed to advance. The
	// library flags it; record it rather than silently accepting.
	if credential.Authenticator.CloneWarning {
		log.Printf("mfa: sign count did not advance for user %s, possible cloned authenticator", userID)
	}
	if err := h.DB.TouchWebAuthnCredential(ctx, credential.ID, credential.Authenticator.SignCount); err != nil {
		log.Printf("mfa: recording passkey use: %v", err)
	}

	h.clearMFAFailures(ctx, userID)
	h.issueSessionAfterMFA(c, user.user, "webauthn")
}

// ---------- Backup codes ----------

func (h *Handler) RegenerateBackupCodes(c *gin.Context) {
	userID := c.GetString("userId")

	enrollments, err := h.DB.ListEnrollments(c.Request.Context(), userID)
	if err != nil || len(enrollments) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "enrol a second factor first"})
		return
	}

	// Gated like a removal, and for a sharper reason: someone on a stolen session who
	// regenerates codes walks away with credentials that survive the password change the
	// victim makes afterwards.
	if !h.requireReauth(c, userID) {
		return
	}

	codes, err := h.issueBackupCodes(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"backupCodes": codes})
	h.auditLog(c, "mfa_regenerate_backup_codes", "user", userID, userID, "", "", nil)
}

func (h *Handler) issueBackupCodes(ctx context.Context, userID string) ([]string, error) {
	codes, err := mfa.GenerateBackupCodes()
	if err != nil {
		return nil, err
	}
	if err := h.DB.ReplaceBackupCodes(ctx, userID, codes); err != nil {
		return nil, err
	}
	return codes, nil
}

// ---------- Admin policy ----------

func (h *Handler) GetMFAPolicy(c *gin.Context) {
	policy := h.mfaPolicy(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"requireAdmin": policy.RequireAdmin})
}

// UpdateMFAPolicy changes who must hold a second factor.
func (h *Handler) UpdateMFAPolicy(c *gin.Context) {
	var req struct {
		RequireAdmin *bool `json:"requireAdmin" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	value := "false"
	if *req.RequireAdmin {
		value = "true"
	}
	if err := h.DB.SetSetting(c.Request.Context(), db.SettingMFARequireAdmin, value, c.GetString("userId")); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"requireAdmin": *req.RequireAdmin})
	h.auditLog(c, "mfa_policy_update", "setting", db.SettingMFARequireAdmin, "mfa policy", "", "",
		map[string]interface{}{"requireAdmin": *req.RequireAdmin})
}

// ---------- Shared helpers ----------

// issueSessionAfterMFA mints the full session that completes a challenge.
func (h *Handler) issueSessionAfterMFA(c *gin.Context, user *db.User, method string) {
	token, expiresAt, err := h.generateScopedJWT(user, middleware.TokenTypeSession, sessionTTL, []string{"pwd", method})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.AuthTokenResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		User: models.UserResponse{
			ID: user.ID, Username: user.Username, Email: user.Email,
			DisplayName: user.DisplayName, Role: user.Role,
		},
	})
	h.auditLog(c, "mfa_verify", "user", user.ID, user.Username, "", "", map[string]interface{}{"method": method})
}

// guardLastFactor applies the anti-lockout rule before a factor is removed.
func (h *Handler) guardLastFactor(c *gin.Context, userID string, removing mfa.Method, credentialID string) error {
	user, err := h.DB.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		return err
	}
	enrollments, err := h.DB.ListEnrollments(c.Request.Context(), userID)
	if err != nil {
		return err
	}

	remaining := make([]mfa.Method, 0, len(enrollments))
	for _, e := range enrollments {
		if e.Method == removing && (credentialID == "" || e.ID == credentialID) {
			continue
		}
		remaining = append(remaining, e.Method)
	}
	return mfa.CanRemoveMethod(user.Role, remaining, h.mfaPolicy(c.Request.Context()))
}

// clearBackupCodesIfNoFactors drops recovery codes once the last factor is gone, so they
// cannot be used to satisfy a challenge for an account that no longer has 2FA.
func (h *Handler) clearBackupCodesIfNoFactors(ctx context.Context, userID string) {
	enrollments, err := h.DB.ListEnrollments(ctx, userID)
	if err == nil && len(enrollments) == 0 {
		if err := h.DB.DeleteBackupCodes(ctx, userID); err != nil {
			log.Printf("mfa: clearing backup codes: %v", err)
		}
	}
}

func (h *Handler) lockedOut(ctx context.Context, userID string) (time.Duration, bool) {
	state, err := h.DB.GetLockout(ctx, userID)
	if err != nil {
		return 0, false
	}
	wait := mfa.LockedFor(state, time.Now())
	return wait, wait > 0
}

func (h *Handler) recordMFAFailure(c *gin.Context, userID string) {
	ctx := c.Request.Context()
	state, err := h.DB.GetLockout(ctx, userID)
	if err != nil {
		return
	}
	next := mfa.NextLockout(state, time.Now())
	if err := h.DB.SaveLockout(ctx, userID, next); err != nil {
		log.Printf("mfa: saving lockout state: %v", err)
	}
	if next.LockedUntil != nil {
		h.auditLog(c, "mfa_lockout", "user", userID, userID, "", "",
			map[string]interface{}{"failedCount": next.FailedCount, "until": next.LockedUntil})
	}
}

func (h *Handler) clearMFAFailures(ctx context.Context, userID string) {
	if err := h.DB.SaveLockout(ctx, userID, mfa.ClearLockout(time.Now())); err != nil {
		log.Printf("mfa: clearing lockout state: %v", err)
	}
}

// describeWebAuthnError surfaces the library's detail, which explains what the browser
// actually rejected, without leaking internals for unexpected failures.
func describeWebAuthnError(err error) string {
	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) && protocolErr.Details != "" {
		return protocolErr.Details
	}
	return "the passkey could not be registered"
}
