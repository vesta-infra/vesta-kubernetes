package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/git"
	"kubernetes.getvesta.sh/api/internal/models"
)

// Listing repositories and branches across every configured connection.
//
// These used to call the one GitHub App directly and, on any failure, answer HTTP 200 with
// an empty list. A picker fed from that cannot tell "this credential sees no repositories"
// from "the call failed", and neither could the user: the dropdown was simply empty, with
// nothing said about why.

// RepoListItem is one repository, tagged with the connection that can reach it so choosing
// it records provider, host and connection together.
type RepoListItem struct {
	FullName     string `json:"full_name"`
	Provider     string `json:"provider"`
	Host         string `json:"host"`
	Private      bool   `json:"private"`
	DefaultRef   string `json:"defaultBranch,omitempty"`
	ConnectionID string `json:"connectionId"`
	Connection   string `json:"connectionName"`
}

// ListAccessibleRepos fans out across connections.
//
// A failure on one connection does not empty the list: several connections is the point, and
// one expired token should not hide every repository the others can see. What each one could
// not do is reported alongside the results.
func (h *Handler) ListAccessibleRepos(c *gin.Context) {
	conns, err := h.DB.ListGitConnections(c.Request.Context(), c.Query("provider"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	repos := []RepoListItem{}
	problems := []gin.H{}
	seen := map[string]bool{}

	for _, conn := range conns {
		impl, ok := h.GitProviders.Get(conn.Provider)
		if !ok {
			continue
		}

		found, err := impl.ListRepos(c.Request.Context(), toGitConnection(conn))
		if err != nil {
			log.Printf("[git] listing repositories for connection %s: %v", conn.ID, err)
			problems = append(problems, gin.H{
				"connectionId":   conn.ID,
				"connectionName": conn.DisplayName,
				"error":          err.Error(),
			})
			continue
		}

		for _, r := range found {
			// Two connections to the same host can both see a repository; offering it
			// twice would make the picker look broken.
			if seen[r.Ref.Key()] {
				continue
			}
			seen[r.Ref.Key()] = true
			repos = append(repos, RepoListItem{
				FullName:     r.Ref.Path,
				Provider:     r.Ref.Provider,
				Host:         r.Ref.Host,
				Private:      r.Private,
				DefaultRef:   r.DefaultRef,
				ConnectionID: conn.ID,
				Connection:   conn.DisplayName,
			})
		}
	}

	// Only a total failure is an error. Partial results with a note about the rest are
	// more useful than nothing.
	if len(repos) == 0 && len(problems) > 0 {
		c.JSON(http.StatusBadGateway, gin.H{
			"repos":    repos,
			"problems": problems,
			"message":  "no connection could list repositories",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"repos": repos, "problems": problems})
}

// ListRepoBranches lists the branches of one repository.
func (h *Handler) ListRepoBranches(c *gin.Context) {
	repo := c.Query("repo")
	if repo == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: "repo query parameter is required"})
		return
	}

	provider := c.Query("provider")
	if provider == "" {
		provider = git.ProviderGitHub
	}

	ref, err := git.ParseRepoRef(provider, c.Query("host"), repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	impl, ok := h.GitProviders.Get(ref.Provider)
	if !ok {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Code: 400, Message: "no implementation for provider " + ref.Provider})
		return
	}

	conn := h.connectionFor(c.Request.Context(), ref, c.Query("connectionId"))
	branches, err := impl.ListBranches(c.Request.Context(), conn, ref)
	if err != nil {
		// Reported, not swallowed. "This repository has no branches" is not a thing, so an
		// empty list here was always a lie.
		c.JSON(http.StatusBadGateway, models.ErrorResponse{Code: 502, Message: err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"branches": branches})
}
