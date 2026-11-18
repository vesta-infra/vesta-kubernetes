package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/rbac"
)

// GetMyPermissions returns every role the caller holds, in one round trip.
//
// The UI used to decide what to show from a single global role cached in localStorage, which
// could only ever answer "admin, developer or viewer" and knew nothing about projects. One
// request here replaces that, and replaces the alternative of asking per project as each
// panel renders.
//
// This is advisory. The server refuses the actions it gates regardless of what the UI chose
// to draw; the point is not showing people buttons that will fail.
func (h *Handler) GetMyPermissions(c *gin.Context) {
	userID := c.GetString("userId")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Code: 401, Message: "not authenticated"})
		return
	}

	globalRole := c.GetString("role")
	enforced := h.DB.GetBoolSetting(c.Request.Context(), db.SettingRBACEnforcement, false)

	scopes, err := h.DB.GetUserScopes(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"global":       globalRole,
		"projects":     scopes.Projects,
		"environments": scopes.Environments,
		// While enforcement is off the memberships above are not consulted, so the UI has
		// to know that to avoid hiding things the server would in fact allow.
		"enforced": enforced,
		// What the caller effectively holds where they have no membership at all.
		"fallback": rbac.LegacyFallback(globalRole),
		"actions":  rbac.AssignableRoles(),
	})
}

// ListProjectEnvMembers returns every environment-scoped role in a project.
func (h *Handler) ListProjectEnvMembers(c *gin.Context) {
	members, err := h.DB.ListProjectEnvMembers(c.Request.Context(), c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.ListResponse{Items: members, Total: len(members)})
}

// SetProjectEnvRole grants or changes a user's role in one environment.
func (h *Handler) SetProjectEnvRole(c *gin.Context) {
	projectID := c.Param("projectId")
	environment := c.Param("env")
	userID := c.Param("userId")

	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	if !rbac.Valid(req.Role) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code: 400, Message: "invalid role: must be one of owner, maintainer, deployer, viewer"})
		return
	}

	if err := h.DB.SetProjectEnvRole(c.Request.Context(), projectID, environment, userID, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "set_env_role", "project", projectID, projectID, projectID, environment,
		map[string]interface{}{"userId": userID, "role": req.Role})

	c.JSON(http.StatusOK, gin.H{
		"projectId": projectID, "environment": environment, "userId": userID, "role": req.Role,
	})
}

// RemoveProjectEnvRole drops an environment override.
//
// Removing is not the same as setting viewer: with no row the project role applies again,
// which is the difference between "restricted here" and "whatever they have on the project".
func (h *Handler) RemoveProjectEnvRole(c *gin.Context) {
	projectID := c.Param("projectId")
	environment := c.Param("env")
	userID := c.Param("userId")

	if err := h.DB.RemoveProjectEnvRole(c.Request.Context(), projectID, environment, userID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no role set for that environment"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "remove_env_role", "project", projectID, projectID, projectID, environment,
		map[string]interface{}{"userId": userID})

	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

// GetRBACSettings reports whether memberships are enforced, and what switching it on would
// cost.
//
// The cost is computed, not sampled. A shadow mode that records denials only sees the users
// who happened to make a request, so a fortnightly contributor looks like nobody -- and
// finding out otherwise means an incident. Counting memberships answers the question
// directly.
func (h *Handler) GetRBACSettings(c *gin.Context) {
	enforced := h.DB.GetBoolSetting(c.Request.Context(), db.SettingRBACEnforcement, false)

	impact, err := h.DB.UsersWithoutMemberships(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	wouldLose := []db.AccessImpact{}
	for _, u := range impact {
		if u.ProjectCount == 0 && u.EnvCount == 0 {
			wouldLose = append(wouldLose, u)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"enforced": enforced,
		"users":    impact,
		// Users who hold no membership anywhere. Once enforced they can reach no project at
		// all, whatever their global role says.
		"wouldLoseAllAccess": wouldLose,
	})
}

// UpdateRBACSettings turns membership enforcement on or off.
func (h *Handler) UpdateRBACSettings(c *gin.Context) {
	var req struct {
		Enforced *bool `json:"enforced" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	value := "false"
	if *req.Enforced {
		value = "true"
	}
	if err := h.DB.SetSetting(c.Request.Context(), db.SettingRBACEnforcement, value, c.GetString("userId")); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "update_rbac_enforcement", "setting", db.SettingRBACEnforcement, "", "", "",
		map[string]interface{}{"enforced": *req.Enforced})

	c.JSON(http.StatusOK, gin.H{"enforced": *req.Enforced})
}
