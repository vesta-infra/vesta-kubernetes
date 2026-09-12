package db

import (
	"context"
	"database/sql"
	"errors"
)

func (d *DB) CreateTeam(ctx context.Context, name, displayName string) (*Team, error) {
	t := &Team{}
	err := d.QueryRowContext(ctx,
		`INSERT INTO teams (name, display_name) VALUES ($1, $2)
		 RETURNING id, name, display_name, created_at`,
		name, displayName,
	).Scan(&t.ID, &t.Name, &t.DisplayName, &t.CreatedAt)

	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return t, nil
}

func (d *DB) GetTeam(ctx context.Context, id string) (*Team, error) {
	t := &Team{}
	err := d.QueryRowContext(ctx,
		`SELECT id, name, display_name, created_at FROM teams WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.DisplayName, &t.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (d *DB) GetTeamByName(ctx context.Context, name string) (*Team, error) {
	t := &Team{}
	err := d.QueryRowContext(ctx,
		`SELECT id, name, display_name, created_at FROM teams WHERE name = $1`, name,
	).Scan(&t.ID, &t.Name, &t.DisplayName, &t.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (d *DB) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, name, display_name, created_at FROM teams ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.DisplayName, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func (d *DB) ListTeamsForUser(ctx context.Context, userID string) ([]Team, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT t.id, t.name, t.display_name, t.created_at
		 FROM teams t
		 JOIN team_members tm ON t.id = tm.team_id
		 WHERE tm.user_id = $1
		 ORDER BY t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.DisplayName, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func (d *DB) UpdateTeam(ctx context.Context, id, displayName string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE teams SET display_name = $2 WHERE id = $1`, id, displayName)
	return err
}

func (d *DB) DeleteTeam(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM teams WHERE id = $1`, id)
	return err
}

func (d *DB) AddTeamMember(ctx context.Context, teamID, userID, role string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (team_id, user_id) DO UPDATE SET role = $3`,
		teamID, userID, role)
	return err
}

func (d *DB) RemoveTeamMember(ctx context.Context, teamID, userID string) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`,
		teamID, userID)
	return err
}

func (d *DB) ListTeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT tm.team_id, tm.user_id, tm.role, tm.joined_at,
		        u.username, u.email, u.display_name
		 FROM team_members tm
		 JOIN users u ON tm.user_id = u.id
		 WHERE tm.team_id = $1
		 ORDER BY tm.joined_at`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Role, &m.JoinedAt,
			&m.Username, &m.Email, &m.DisplayName); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
