package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/mfa"
	"kubernetes.getvesta.sh/api/internal/middleware"
	"kubernetes.getvesta.sh/api/internal/models"
)

// publicBaseURL returns the externally reachable base URL of this Vesta instance, with
// any trailing slash removed. Links in outbound email must be built from this rather
// than from request headers, which the caller controls.
func publicBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("VESTA_PUBLIC_URL")), "/")
}

// validAPITokenScopes is the complete set an API token may carry.
var validAPITokenScopes = []string{"read", "write", "deploy", "admin"}

// defaultAPITokenScopes is what a token gets when the caller asks for nothing specific.
var defaultAPITokenScopes = []string{"deploy", "read"}

// validateTokenScopes checks requested scopes against the allowlist and refuses to mint a
// token more powerful than the requesting user's own role.
//
// This previously took req.Scopes verbatim, and middleware.RequireScope treats the
// "admin" scope as satisfying every scope check - so any developer could mint themselves
// a 90-day admin-scoped key. Scopes are normalised and de-duplicated so that "Admin" or a
// repeated entry cannot slip past the comparison.
func validateTokenScopes(requested []string, userRole string) ([]string, error) {
	out := make([]string, 0, len(requested))
	for _, s := range requested {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !slices.Contains(validAPITokenScopes, s) {
			return nil, fmt.Errorf("unknown scope %q: valid scopes are %s", s, strings.Join(validAPITokenScopes, ", "))
		}
		switch {
		case s == "admin" && userRole != "admin":
			return nil, fmt.Errorf("scope %q requires the admin role", s)
		case (s == "write" || s == "deploy") && userRole == "viewer":
			return nil, fmt.Errorf("scope %q is not available to the viewer role", s)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}

	if len(out) == 0 {
		if userRole == "viewer" {
			return []string{"read"}, nil
		}
		return slices.Clone(defaultAPITokenScopes), nil
	}
	return out, nil
}

func (h *Handler) SetupStatus(c *gin.Context) {
	count, err := h.DB.UserCount(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	c.JSON(http.StatusOK, models.SetupStatusResponse{NeedsSetup: count == 0})
}

func (h *Handler) Setup(c *gin.Context) {
	count, err := h.DB.UserCount(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	if count > 0 {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "setup already completed"})
		return
	}

	var req models.SetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}

	user, err := h.DB.CreateUser(c.Request.Context(), req.Email, req.Username, req.Password, displayName, "admin")
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "user already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	teamDisplayName := req.TeamName
	team, err := h.DB.CreateTeam(c.Request.Context(), slugify(req.TeamName), teamDisplayName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to create team: " + err.Error()})
		return
	}

	if err := h.DB.AddTeamMember(c.Request.Context(), team.ID, user.ID, "owner"); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to add team member"})
		return
	}

	tokenString, expiresAt, err := h.generateJWT(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate token"})
		return
	}

	c.JSON(http.StatusCreated, models.AuthTokenResponse{
		Token:     tokenString,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		User: models.UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
	})
}

func (h *Handler) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	user, err := h.DB.GetUserByUsername(c.Request.Context(), req.Username)
	if errors.Is(err, db.ErrNotFound) {
		user, err = h.DB.GetUserByEmail(c.Request.Context(), req.Username)
	}
	if err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "invalid credentials"})
		return
	}

	if !db.CheckPassword(user.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "invalid credentials"})
		return
	}

	// A correct password is only the first factor. Anyone holding one already stops here
	// if the account carries a second, or if policy says it must.
	if h.issuePartialTokenIfNeeded(c, user) {
		return
	}

	tokenString, expiresAt, err := h.generateJWT(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.AuthTokenResponse{
		Token:     tokenString,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		User: models.UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
	})
}

// issuePartialTokenIfNeeded interrupts login when a second factor is owed, and reports
// whether it has already written a response.
//
// Two distinct interruptions share this path. A user who holds a factor gets an
// mfa_challenge token, which reaches only the verification endpoints. A user who holds
// none but whose role requires one gets an mfa_enroll token, which reaches only the
// enrollment endpoints -- that is what makes a policy change take effect for people who
// were already registered, without locking them out.
//
// Neither token carries a role or team list, and neither is accepted anywhere else: see
// middleware.RequireFullSession.
func (h *Handler) issuePartialTokenIfNeeded(c *gin.Context, user *db.User) bool {
	ctx := c.Request.Context()

	enrollments, err := h.DB.ListEnrollments(ctx, user.ID)
	if err != nil {
		// Failing open here would let a database blip skip the second factor entirely.
		c.JSON(http.StatusServiceUnavailable, models.ErrorResponse{
			Code:    503,
			Message: "cannot verify two-factor status, try again",
		})
		return true
	}

	if len(enrollments) > 0 {
		token, expiresAt, err := h.generateScopedJWT(user, middleware.TokenTypeMFAChallenge, mfaChallengeTTL, []string{"pwd"})
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate token"})
			return true
		}
		c.JSON(http.StatusOK, gin.H{
			"mfaRequired": true,
			"token":       token,
			"expiresAt":   expiresAt.Format(time.RFC3339),
			"methods":     mfa.Methods(enrollments),
		})
		return true
	}

	if mfa.RequiredFor(user.Role, h.mfaPolicy(ctx)) {
		token, expiresAt, err := h.generateScopedJWT(user, middleware.TokenTypeMFAEnroll, mfaEnrollTTL, []string{"pwd"})
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate token"})
			return true
		}
		c.JSON(http.StatusOK, gin.H{
			"mfaEnrollmentRequired": true,
			"token":                 token,
			"expiresAt":             expiresAt.Format(time.RFC3339),
			"reason":                fmt.Sprintf("two-factor authentication is required for the %s role", user.Role),
			"totpAvailable":         totpCipher() != nil,
		})
		return true
	}

	return false
}

func (h *Handler) Register(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	role := req.Role
	if role == "" {
		role = "developer"
	}
	if role != "admin" && role != "developer" && role != "viewer" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid role"})
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}

	// If no password provided, create with a random one (user will set via invite link)
	password := req.Password
	isInvite := password == ""
	if isInvite {
		tokenBytes := make([]byte, 32)
		rand.Read(tokenBytes)
		password = hex.EncodeToString(tokenBytes) // Placeholder password — will be reset
	}

	user, err := h.DB.CreateUser(c.Request.Context(), req.Email, req.Username, password, displayName, role)
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "user already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	// Generate invite token if this is a passwordless invite
	var inviteToken string
	if isInvite {
		tokenBytes := make([]byte, 32)
		rand.Read(tokenBytes)
		inviteToken = hex.EncodeToString(tokenBytes)
		tokenHash := db.HashToken(inviteToken)
		expiresAt := time.Now().Add(7 * 24 * time.Hour) // 7 days
		if err := h.DB.CreateInviteToken(c.Request.Context(), user.ID, tokenHash, expiresAt); err != nil {
			// User created but token failed — still return success
			inviteToken = ""
		}
	}

	resp := gin.H{
		"id":          user.ID,
		"username":    user.Username,
		"email":       user.Email,
		"displayName": user.DisplayName,
		"role":        user.Role,
	}
	if inviteToken != "" {
		resp["inviteToken"] = inviteToken
	}
	c.JSON(http.StatusCreated, resp)

	// Send invite email asynchronously if an email channel is configured.
	//
	// The base URL comes from configuration, never from the request. This used to read
	// the Origin header, so an admin induced to call this endpoint from a page an
	// attacker controlled would send an invite email pointing at the attacker's host,
	// phishing the invitee's password.
	loginURL := ""
	if base := publicBaseURL(); base != "" && inviteToken != "" {
		loginURL = base + "/accept-invite?token=" + inviteToken
	}
	go func() {
		if err := h.Notifier.SendInviteEmail(user.Email, user.Username, user.Role, loginURL); err != nil {
			// Log but don't fail — the user was already created
			_ = err
		}
	}()
}

func (h *Handler) AcceptInvite(c *gin.Context) {
	var req struct {
		Token    string `json:"token" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "password must be at least 8 characters"})
		return
	}

	// Consume the token before setting the password: this both validates it and marks it
	// used in one statement, so it cannot be replayed. Previously those were two steps
	// and the mark's error was discarded, leaving the token usable for its full life.
	user, err := h.DB.ConsumeInviteToken(c.Request.Context(), db.HashToken(req.Token))
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "invalid or expired invite token"})
		return
	}

	if err := h.DB.UpdateUserPassword(c.Request.Context(), user.ID, req.Password); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to set password"})
		return
	}

	// Generate JWT so user is logged in immediately
	tokenString, expiresAt, err := h.generateJWT(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, models.AuthTokenResponse{
		Token:     tokenString,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		User: models.UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
	})
}

func (h *Handler) GetCurrentUser(c *gin.Context) {
	userID := c.GetString("userId")
	user, err := h.DB.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	c.JSON(http.StatusOK, models.UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
	})
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	userID := c.GetString("userId")
	var req models.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if err := h.DB.UpdateUserProfile(c.Request.Context(), userID, req.DisplayName, req.Email); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "profile updated"})
}

func (h *Handler) ChangePassword(c *gin.Context) {
	userID := c.GetString("userId")
	var req models.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	user, err := h.DB.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "user not found"})
		return
	}

	if !db.CheckPassword(user.PasswordHash, req.CurrentPassword) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "current password is incorrect"})
		return
	}

	if err := h.DB.UpdateUserPassword(c.Request.Context(), userID, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password changed"})
}

func (h *Handler) ListUsers(c *gin.Context) {
	users, err := h.DB.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]models.UserResponse, len(users))
	for i, u := range users {
		items[i] = models.UserResponse{
			ID: u.ID, Username: u.Username, Email: u.Email,
			DisplayName: u.DisplayName, Role: u.Role,
		}
	}
	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) CreateAPIToken(c *gin.Context) {
	userID := c.GetString("userId")
	var req models.CreateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	scopes, err := validateTokenScopes(req.Scopes, c.GetString("role"))
	if err != nil {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Code: 403, Message: err.Error()})
		return
	}

	rawToken := generateRandomToken()
	tokenHash := db.HashToken(rawToken)

	var expiresAt *time.Time
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err == nil {
			t := time.Now().Add(d)
			expiresAt = &t
		}
	}
	if expiresAt == nil {
		t := time.Now().Add(90 * 24 * time.Hour)
		expiresAt = &t
	}

	token, err := h.DB.CreateAPIToken(c.Request.Context(), userID, req.Name, tokenHash, scopes, expiresAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	resp := models.APITokenCreatedResponse{
		ID:     token.ID,
		Name:   token.Name,
		Token:  rawToken,
		Scopes: scopes,
	}
	if token.ExpiresAt != nil {
		resp.ExpiresAt = token.ExpiresAt.Format(time.RFC3339)
	}
	c.JSON(http.StatusCreated, resp)
}

func (h *Handler) RevokeAPIToken(c *gin.Context) {
	userID := c.GetString("userId")
	tokenID := c.Param("id")

	if err := h.DB.RevokeAPIToken(c.Request.Context(), tokenID, userID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "token not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListAPITokens(c *gin.Context) {
	userID := c.GetString("userId")
	tokens, err := h.DB.ListAPITokens(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]models.APITokenResponse, len(tokens))
	for i, t := range tokens {
		items[i] = models.APITokenResponse{
			ID:        t.ID,
			Name:      t.Name,
			Scopes:    t.Scopes,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if t.ExpiresAt != nil {
			s := t.ExpiresAt.Format(time.RFC3339)
			items[i].ExpiresAt = &s
		}
		if t.LastUsedAt != nil {
			s := t.LastUsedAt.Format(time.RFC3339)
			items[i].LastUsedAt = &s
		}
	}
	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) OAuthRedirect(c *gin.Context) {
	provider := c.Param("provider")
	c.JSON(http.StatusNotImplemented, gin.H{
		"provider": provider,
		"message":  "OAuth2 not yet implemented",
	})
}

// Token lifetimes.
const (
	// sessionTTL is how long a fully authenticated session lasts.
	sessionTTL = 24 * time.Hour
	// mfaChallengeTTL bounds the gap between a correct password and a second factor.
	mfaChallengeTTL = 5 * time.Minute
	// mfaEnrollTTL is longer because enrolling means scanning a QR code, typing a code,
	// and saving recovery codes.
	mfaEnrollTTL = 15 * time.Minute
)

// generateJWT issues a fully authenticated session token.
func (h *Handler) generateJWT(user *db.User) (string, time.Time, error) {
	return h.generateScopedJWT(user, middleware.TokenTypeSession, sessionTTL, []string{"pwd"})
}

// generateScopedJWT issues a token of a specific type.
//
// Only session tokens carry role and teamIds. Omitting them from partial tokens is
// defence in depth rather than the control itself - RequireRole and RequireProjectRole
// read the role from the context and so fail closed without it, but DenyRole is
// allow-by-default and RequireScope waves JWTs straight through, so neither can be
// relied on. The token-type gate in AuthRequired is the actual control.
//
// amr records how the user authenticated ("pwd", "otp", "webauthn", "backup"), which is
// what lets a later step-up check tell a password-only session from a two-factor one.
func (h *Handler) generateScopedJWT(user *db.User, typ middleware.TokenType, ttl time.Duration, amr []string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)

	jti, err := generateJTI()
	if err != nil {
		return "", time.Time{}, err
	}

	claims := jwt.MapClaims{
		"sub": user.ID,
		"typ": string(typ),
		"amr": amr,
		"jti": jti,
		"exp": expiresAt.Unix(),
		"iat": now.Unix(),
	}

	if typ == middleware.TokenTypeSession {
		teamIDs, _ := h.DB.GetUserTeamIDs(context.Background(), user.ID)
		claims["role"] = user.Role
		claims["teamIds"] = teamIDs
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(middleware.GetJWTSecret())
	return tokenString, expiresAt, err
}

// generateJTI returns a unique token identifier, so individual tokens can be named in
// audit entries and denied later.
func generateJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func generateRandomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return "vst_" + hex.EncodeToString(b)
}

func slugify(s string) string {
	result := make([]byte, 0, len(s))
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, byte(c))
		} else if c >= 'A' && c <= 'Z' {
			result = append(result, byte(c+32))
		} else if c == ' ' || c == '_' {
			result = append(result, '-')
		}
	}
	return string(result)
}

// ForgotPasswordStatus returns whether forgot password is available (email channel configured).
func (h *Handler) ForgotPasswordStatus(c *gin.Context) {
	has, err := h.DB.HasEmailChannel()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"available": has})
}

// ForgotPassword generates a reset token and emails it. Only works if an email channel is configured.
func (h *Handler) ForgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Always return success to prevent email enumeration
	successMsg := gin.H{"message": "If an account with that email exists, a reset code has been sent."}

	user, err := h.DB.GetUserByEmail(c.Request.Context(), req.Email)
	if err != nil {
		c.JSON(http.StatusOK, successMsg)
		return
	}

	rawToken := generateRandomToken()
	tokenHash := db.HashToken(rawToken)
	expiresAt := time.Now().Add(1 * time.Hour)

	if err := h.DB.CreatePasswordResetToken(c.Request.Context(), user.ID, tokenHash, expiresAt); err != nil {
		c.JSON(http.StatusOK, successMsg)
		return
	}

	go func() {
		if err := h.Notifier.SendPasswordResetEmail(user.Email, rawToken); err != nil {
			// Log but don't expose to user
			_ = err
		}
	}()

	c.JSON(http.StatusOK, successMsg)
}

// ResetPassword validates the reset token and sets the new password.
func (h *Handler) ResetPassword(c *gin.Context) {
	var req struct {
		Token       string `json:"token" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	tokenHash := db.HashToken(req.Token)
	userID, err := h.DB.ValidatePasswordResetToken(c.Request.Context(), tokenHash)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "Invalid or expired reset token"})
		return
	}

	if err := h.DB.UpdateUserPassword(c.Request.Context(), userID, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: "Failed to update password"})
		return
	}

	_ = h.DB.ConsumePasswordResetToken(c.Request.Context(), tokenHash)

	c.JSON(http.StatusOK, gin.H{"message": "Password has been reset. You can now log in."})
}
