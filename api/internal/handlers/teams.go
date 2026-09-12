package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/db"
	"kubernetes.getvesta.sh/api/internal/models"
)

func (h *Handler) CreateTeam(c *gin.Context) {
	var req models.CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Name
	}

	team, err := h.DB.CreateTeam(c.Request.Context(), req.Name, displayName)
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409, Message: "team already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, models.TeamResponse{
		ID:          team.ID,
		Name:        team.Name,
		DisplayName: team.DisplayName,
		CreatedAt:   team.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (h *Handler) ListTeams(c *gin.Context) {
	role, _ := c.Get("role")
	userID := c.GetString("userId")

	var teams []db.Team
	var err error

	if role == "admin" {
		teams, err = h.DB.ListTeams(c.Request.Context())
	} else {
		teams, err = h.DB.ListTeamsForUser(c.Request.Context(), userID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	items := make([]models.TeamResponse, len(teams))
	for i, t := range teams {
		members, _ := h.DB.ListTeamMembers(c.Request.Context(), t.ID)
		items[i] = models.TeamResponse{
			ID:          t.ID,
			Name:        t.Name,
			DisplayName: t.DisplayName,
			MemberCount: len(members),
			CreatedAt:   t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	c.JSON(http.StatusOK, models.ListResponse{Items: items, Total: len(items)})
}

func (h *Handler) GetTeam(c *gin.Context) {
	teamID := c.Param("teamId")
	team, err := h.DB.GetTeam(c.Request.Context(), teamID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "team not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	members, _ := h.DB.ListTeamMembers(c.Request.Context(), teamID)
	memberResp := make([]models.TeamMemberResponse, len(members))
	for i, m := range members {
		memberResp[i] = models.TeamMemberResponse{
			UserID:      m.UserID,
			Username:    m.Username,
			Email:       m.Email,
			DisplayName: m.DisplayName,
			Role:        m.Role,
			JoinedAt:    m.JoinedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          team.ID,
		"name":        team.Name,
		"displayName": team.DisplayName,
		"createdAt":   team.CreatedAt.Format("2006-01-02T15:04:05Z"),
		"members":     memberResp,
	})
}

func (h *Handler) UpdateTeam(c *gin.Context) {
	teamID := c.Param("teamId")
	var req models.UpdateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if err := h.DB.UpdateTeam(c.Request.Context(), teamID, req.DisplayName); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "team updated"})
}

func (h *Handler) DeleteTeam(c *gin.Context) {
	teamID := c.Param("teamId")
	if err := h.DB.DeleteTeam(c.Request.Context(), teamID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) AddTeamMember(c *gin.Context) {
	teamID := c.Param("teamId")
	var req models.AddTeamMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	role := req.Role
	if role == "" {
		role = "member"
	}

	if err := h.DB.AddTeamMember(c.Request.Context(), teamID, req.UserID, role); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "member added"})
}

func (h *Handler) RemoveTeamMember(c *gin.Context) {
	teamID := c.Param("teamId")
	userID := c.Param("userId")

	if err := h.DB.RemoveTeamMember(c.Request.Context(), teamID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
