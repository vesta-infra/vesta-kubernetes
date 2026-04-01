package middleware

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"kubernetes.getvesta.sh/api/internal/db"
)

func GetJWTSecret() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "vesta-jwt-secret"
	}
	return []byte(secret)
}

func AuthRequired(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		// WebSocket connections can't send custom headers, so accept token as query param
		if authHeader == "" {
			if token := c.Query("token"); token != "" {
				authHeader = "Bearer " + token
			}
		}

		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "authorization header required",
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "invalid authorization header format, expected: Bearer <token>",
			})
			return
		}

		tokenString := parts[1]

		// API key authentication (vst_ prefix)
		if strings.HasPrefix(tokenString, "vst_") {
			authenticateAPIKey(c, database, tokenString)
			return
		}

		// JWT authentication
		secret := GetJWTSecret()

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return secret, nil
		})

		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "invalid or expired token",
			})
			return
		}

		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			c.Set("userId", claims["sub"])
			c.Set("role", claims["role"])
			c.Set("authType", "jwt")
			if teamIds, ok := claims["teamIds"].([]interface{}); ok {
				ids := make([]string, len(teamIds))
				for i, v := range teamIds {
					ids[i], _ = v.(string)
				}
				c.Set("teamIds", ids)
			}
		}

		c.Next()
	}
}

func authenticateAPIKey(c *gin.Context, database *db.DB, rawToken string) {
	tokenHash := db.HashToken(rawToken)
	apiToken, err := database.GetAPITokenByHash(c.Request.Context(), tokenHash)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code":    401,
			"message": "invalid API key",
		})
		return
	}

	// Check expiration
	if apiToken.ExpiresAt != nil && apiToken.ExpiresAt.Before(time.Now()) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code":    401,
			"message": "API key has expired",
		})
		return
	}

	// Look up the owning user to get their role
	user, err := database.GetUserByID(c.Request.Context(), apiToken.UserID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code":    401,
			"message": "API key owner not found",
		})
		return
	}

	c.Set("userId", user.ID)
	c.Set("role", user.Role)
	c.Set("authType", "apikey")
	c.Set("tokenScopes", apiToken.Scopes)
	c.Set("tokenId", apiToken.ID)

	// Update last used timestamp
	go database.TouchAPIToken(c.Request.Context(), apiToken.ID)

	c.Next()
}

func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, _ := c.Get("role")
		roleStr, _ := userRole.(string)

		for _, r := range roles {
			if roleStr == r {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code":    403,
			"message": "insufficient permissions",
		})
	}
}

// RequireScope checks that an API key has one of the required scopes.
// JWT-authenticated users pass through (they have implicit full access).
func RequireScope(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authType, _ := c.Get("authType")
		if authType != "apikey" {
			// JWT users have full access
			c.Next()
			return
		}

		tokenScopes, exists := c.Get("tokenScopes")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "API key has no scopes",
			})
			return
		}

		scopeList, ok := tokenScopes.([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "invalid token scopes",
			})
			return
		}

		for _, required := range scopes {
			for _, have := range scopeList {
				if have == required || have == "admin" {
					c.Next()
					return
				}
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code":    403,
			"message": "API key missing required scope: " + strings.Join(scopes, " or "),
		})
	}
}

// DenyRole blocks users with any of the specified roles.
// Use to prevent viewers from accessing write endpoints.
func DenyRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, _ := c.Get("role")
		roleStr, _ := userRole.(string)

		for _, r := range roles {
			if roleStr == r {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"code":    403,
					"message": "your role does not have access to this resource",
				})
				return
			}
		}

		c.Next()
	}
}

// RequireTeamRole checks that the user has the specified role within the team.
// Admins (global role) always pass. teamId is read from the route param.
func RequireTeamRole(database *db.DB, roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, _ := c.Get("role")
		if userRole == "admin" {
			c.Next()
			return
		}

		teamID := c.Param("teamId")
		userID := c.GetString("userId")
		if teamID == "" || userID == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "insufficient permissions",
			})
			return
		}

		members, err := database.ListTeamMembers(c.Request.Context(), teamID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    500,
				"message": "failed to check team membership",
			})
			return
		}

		for _, m := range members {
			if m.UserID == userID {
				for _, r := range roles {
					if m.Role == r {
						c.Next()
						return
					}
				}
				break
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code":    403,
			"message": "insufficient team permissions",
		})
	}
}
