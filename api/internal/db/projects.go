package db

import (
	"context"
	"database/sql"
	"errors"
)

func (d *DB) AddProjectMember(ctx context.Context, projectID, userID, role string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (project_id, user_id) DO UPDATE SET role = $3`,
		projectID, userID, role)
	return err
}

func (d *DB) RemoveProjectMember(ctx context.Context, projectID, userID string) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, userID)
	return err
}

func (d *DB) ListProjectMembers(ctx context.Context, projectID string) ([]ProjectMember, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT pm.project_id, pm.user_id, pm.role, pm.created_at,
		        u.username, u.email, u.display_name
		 FROM project_members pm
		 JOIN users u ON pm.user_id = u.id
		 WHERE pm.project_id = $1
		 ORDER BY pm.created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []ProjectMember
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Role, &m.CreatedAt,
			&m.Username, &m.Email, &m.DisplayName); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (d *DB) GetProjectMemberRole(ctx context.Context, projectID, userID string) (string, error) {
	var role string
	err := d.QueryRowContext(ctx,
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func (d *DB) IsProjectOwner(ctx context.Context, projectID, userID string) (bool, error) {
	role, err := d.GetProjectMemberRole(ctx, projectID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role == "owner", nil
}
