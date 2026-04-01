package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/middleware"
	"kubernetes.getvesta.sh/api/internal/models"
)

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

	user, err := h.DB.CreateUser(c.Request.Context(), req.Email, req.Username, req.Password, displayName, role)
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "user already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, models.UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
	})

	// Send invite email asynchronously if an email channel is configured
	invitedBy := c.GetString("userId")
	go func() {
		if err := h.Notifier.SendInviteEmail(user.Email, user.Username, user.Role, invitedBy); err != nil {
			// Log but don't fail — the user was already created
			_ = err
		}
	}()
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

	rawToken := generateRandomToken()
	tokenHash := db.HashToken(rawToken)

	scopes := req.Scopes
	if scopes == nil {
		scopes = []string{"deploy", "read"}
	}

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

func (h *Handler) generateJWT(user *db.User) (string, time.Time, error) {
	teamIDs, _ := h.DB.GetUserTeamIDs(context.Background(), user.ID)
	expiresAt := time.Now().Add(24 * time.Hour)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":     user.ID,
		"role":    user.Role,
		"teamIds": teamIDs,
		"exp":     expiresAt.Unix(),
		"iat":     time.Now().Unix(),
	})

	tokenString, err := token.SignedString(middleware.GetJWTSecret())
	return tokenString, expiresAt, err
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
