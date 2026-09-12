package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/webauthn"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
)

// reauthTTL is how long a proof of identity stays spendable. Long enough to read a
// confirmation dialog and find a security key, short enough that a grant left behind in a
// closed tab is worthless.
const reauthTTL = 5 * time.Minute

// ReauthHeader carries the grant id on a request that changes a user's factors.
//
// A header rather than a body field because DELETE requests carry no body in the fetch
// API, and rather than a query parameter because those are routinely written to access
// logs and proxy traces.
const ReauthHeader = "X-Vesta-Reauth"

// ReauthWithPassword exchanges the account password for a single-use grant.
func (h *Handler) ReauthWithPassword(c *gin.Context) {
	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	userID := c.GetString("userId")
	ctx := c.Request.Context()

	user, err := h.DB.GetUserByID(ctx, userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "incorrect password"})
		return
	}

	// Shares the 2FA lockout counter deliberately: password guessing and code guessing
	// against the same account should not each get their own fresh allowance.
	if wait, locked := h.lockedOut(ctx, userID); locked {
		c.JSON(http.StatusTooManyRequests, models.ErrorResponse{
			Code:    429,
			Message: "too many failed attempts, try again in " + wait.Round(time.Second).String(),
		})
		return
	}

	if !db.CheckPassword(user.PasswordHash, req.Password) {
		h.recordMFAFailure(c, userID)
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "incorrect password"})
		return
	}

	h.clearMFAFailures(ctx, userID)
	h.issueReauthGrant(c, userID, "password")
}

// BeginReauthWebAuthn starts a passkey ceremony for re-authentication.
//
// Separate from the login ceremony so the two cannot be crossed: a grant is only ever
// minted by a challenge this endpoint issued, and a login assertion cannot be replayed
// here to authorise a removal.
func (h *Handler) BeginReauthWebAuthn(c *gin.Context) {
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
	sessionID, err := h.DB.CreateWebAuthnSession(c.Request.Context(), userID, "reauth", encoded, time.Now().Add(webauthnCeremonyTTL))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionId": sessionID, "publicKey": assertion.Response})
}

// FinishReauthWebAuthn completes the ceremony and issues a grant.
func (h *Handler) FinishReauthWebAuthn(c *gin.Context) {
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
			Message: "too many failed attempts, try again in " + wait.Round(time.Second).String(),
		})
		return
	}

	raw, err := h.DB.TakeWebAuthnSession(ctx, sessionID, userID, "reauth")
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
	if err := h.DB.TouchWebAuthnCredential(ctx, credential.ID, credential.Authenticator.SignCount); err != nil {
		// Recording usage is bookkeeping; the assertion itself already verified.
		_ = err
	}

	h.clearMFAFailures(ctx, userID)
	h.issueReauthGrant(c, userID, "webauthn")
}

func (h *Handler) issueReauthGrant(c *gin.Context, userID, method string) {
	expiresAt := time.Now().Add(reauthTTL)
	id, err := h.DB.CreateReauthGrant(c.Request.Context(), userID, method, expiresAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"grantId":   id,
		"method":    method,
		"expiresAt": expiresAt.Format(time.RFC3339),
	})
}

// requireReauth spends the grant on the request, and writes the response if there is not
// a valid one.
//
// It reports whether the caller may proceed. Written as a helper rather than middleware
// because the grant must be spent only when the change is actually about to happen: as
// middleware it would burn on requests that then fail validation, and the user would have
// to re-authenticate to retry something that never took effect.
func (h *Handler) requireReauth(c *gin.Context, userID string) bool {
	grantID := c.GetHeader(ReauthHeader)
	if grantID == "" {
		c.JSON(http.StatusForbidden, models.ErrorResponse{
			Code:    403,
			Message: "confirm your identity before changing two-factor settings",
			Details: "reauth_required",
		})
		return false
	}

	if _, err := h.DB.TakeReauthGrant(c.Request.Context(), grantID, userID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusForbidden, models.ErrorResponse{
				Code:    403,
				Message: "that confirmation has expired or was already used",
				Details: "reauth_required",
			})
			return false
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return false
	}
	return true
}

// requireReauthForNewFactor demands a fresh proof of identity before a factor is added to
// an account that already holds one.
//
// Gating removal alone leaves the hole half open: someone on a stolen session who cannot
// take the victim's factor away can still register their own alongside it and keep access
// indefinitely, surviving the password change the victim makes on noticing.
//
// The first factor is deliberately exempt. There is nothing to prove possession of yet,
// and requiring it would make the mandatory-enrollment path at login impossible -- that
// user has no factor by definition, which is why they are being asked for one.
func (h *Handler) requireReauthForNewFactor(c *gin.Context, userID string) bool {
	enrollments, err := h.DB.ListEnrollments(c.Request.Context(), userID)
	if err != nil {
		// Fail closed. Treating an unreadable factor list as "no factors" would turn a
		// database blip into a way past this check.
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{
			Code:    503,
			Message: "cannot read your current factors, try again",
		})
		return false
	}
	if len(enrollments) == 0 {
		return true
	}
	return h.requireReauth(c, userID)
}

// ResetUserMFA clears another user's factors when they can no longer do it themselves.
func (h *Handler) ResetUserMFA(c *gin.Context) {
	targetID := c.Param("userId")
	actorID := c.GetString("userId")

	// An admin resetting themselves here would sidestep the anti-lockout rule and the
	// step-up their own removal path enforces. Send them through it instead.
	if targetID == actorID {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code:    400,
			Message: "use your own two-factor settings to change your factors",
		})
		return
	}

	target, err := h.DB.GetUserByID(c.Request.Context(), targetID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	// Stripping someone else's second factor is as sensitive as removing your own, so it
	// carries the same proof-of-presence requirement.
	if !h.requireReauth(c, actorID) {
		return
	}

	if err := h.DB.ResetUserMFA(c.Request.Context(), targetID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"reset": true,
		"user":  target.Username,
		// Under a mandatory policy the user is not locked out: their next sign-in stops
		// at the enrollment screen instead.
		"mustReenroll": h.mfaPolicy(c.Request.Context()).RequireAdmin && target.Role == "admin",
	})

	h.auditLog(c, "mfa_admin_reset", "user", targetID, target.Username, "", "",
		map[string]interface{}{"resetBy": actorID})
}
