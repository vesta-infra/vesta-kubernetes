package db

import (
	"context"
	"database/sql"
	"errors"
)

// Project and environment memberships.
//
// project_members already existed with a free-text role column, but the handler rejected
// anything except "owner", so the column only ever held one value. Nothing here needed a
// migration for that -- only the validation had to relax.

// ProjectEnvMember is one user's role in one environment of one project.
type ProjectEnvMember struct {
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment"`
	UserID      string `json:"userId"`
	Username    string `json:"username,omitempty"`
	Email       string `json:"email,omitempty"`
	Role        string `json:"role"`
}

// GetProjectEnvRole returns a user's role in one environment, or ErrNotFound when no row
// exists -- which means "inherit the project role", not "no access".
func (d *DB) GetProjectEnvRole(ctx context.Context, projectID, environment, userID string) (string, error) {
	var role string
	// project_id is TEXT (the CR name) while user_id is UUID, so they get separate
	// placeholders; reusing one breaks Postgres type inference.
	err := d.QueryRowContext(ctx, `
		SELECT role FROM project_env_members
		WHERE project_id = $1 AND environment = $2 AND user_id = $3`,
		projectID, environment, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

// SetProjectEnvRole records or updates a user's role in one environment.
func (d *DB) SetProjectEnvRole(ctx context.Context, projectID, environment, userID, role string) error {
	_, err := d.ExecContext(ctx, `
		INSERT INTO project_env_members (project_id, environment, user_id, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, environment, user_id) DO UPDATE SET role = $4`,
		projectID, environment, userID, role)
	return err
}

func (d *DB) RemoveProjectEnvRole(ctx context.Context, projectID, environment, userID string) error {
	_, err := d.ExecContext(ctx, `
		DELETE FROM project_env_members
		WHERE project_id = $1 AND environment = $2 AND user_id = $3`,
		projectID, environment, userID)
	return err
}

// ListProjectEnvMembers returns every environment-scoped role in a project.
func (d *DB) ListProjectEnvMembers(ctx context.Context, projectID string) ([]ProjectEnvMember, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT m.project_id, m.environment, m.user_id, m.role,
		       COALESCE(u.username, ''), COALESCE(u.email, '')
		FROM project_env_members m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE m.project_id = $1
		ORDER BY m.environment, u.username`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProjectEnvMember{}
	for rows.Next() {
		var m ProjectEnvMember
		if err := rows.Scan(&m.ProjectID, &m.Environment, &m.UserID, &m.Role, &m.Username, &m.Email); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UserScopes is every membership one user holds, for answering "what can I do" in a single
// round trip rather than one per project the UI happens to render.
type UserScopes struct {
	Projects     map[string]string `json:"projects"`
	Environments map[string]string `json:"environments"` // keyed "project/environment"
}

// GetUserScopes loads all of a user's project and environment roles.
func (d *DB) GetUserScopes(ctx context.Context, userID string) (UserScopes, error) {
	scopes := UserScopes{
		Projects:     map[string]string{},
		Environments: map[string]string{},
	}

	rows, err := d.QueryContext(ctx,
		`SELECT project_id, role FROM project_members WHERE user_id = $1`, userID)
	if err != nil {
		return scopes, err
	}
	for rows.Next() {
		var project, role string
		if err := rows.Scan(&project, &role); err != nil {
			rows.Close()
			return scopes, err
		}
		scopes.Projects[project] = role
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return scopes, err
	}

	envRows, err := d.QueryContext(ctx,
		`SELECT project_id, environment, role FROM project_env_members WHERE user_id = $1`, userID)
	if err != nil {
		return scopes, err
	}
	defer envRows.Close()

	for envRows.Next() {
		var project, environment, role string
		if err := envRows.Scan(&project, &environment, &role); err != nil {
			return scopes, err
		}
		scopes.Environments[project+"/"+environment] = role
	}
	return scopes, envRows.Err()
}

// AccessImpact describes one user's exposure to enforcement being switched on.
type AccessImpact struct {
	UserID       string `json:"userId"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	GlobalRole   string `json:"globalRole"`
	ProjectCount int    `json:"projectCount"`
	EnvCount     int    `json:"envCount"`
}

// UsersWithoutMemberships lists non-admin users and how many memberships they hold.
//
// This answers "who loses access if enforcement is switched on" directly, by looking at
// what exists rather than by sampling traffic and hoping the quiet users showed up. A user
// with no memberships keeps only what their global role gives them today -- which, once
// enforced, is nothing outside the projects they have been added to.
func (d *DB) UsersWithoutMemberships(ctx context.Context) ([]AccessImpact, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT u.id, u.username, COALESCE(u.email, ''), u.role,
		       (SELECT count(*) FROM project_members m WHERE m.user_id = u.id),
		       (SELECT count(*) FROM project_env_members e WHERE e.user_id = u.id)
		FROM users u
		WHERE u.role <> 'admin'
		ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AccessImpact{}
	for rows.Next() {
		var a AccessImpact
		if err := rows.Scan(&a.UserID, &a.Username, &a.Email, &a.GlobalRole,
			&a.ProjectCount, &a.EnvCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
