package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Build struct {
	ID           string     `json:"id"`
	AppID        string     `json:"appId"`
	ProjectID    string     `json:"projectId"`
	Environment  string     `json:"environment"`
	Status       string     `json:"status"`
	Strategy     string     `json:"strategy"`
	CommitSHA    string     `json:"commitSha"`
	Branch       string     `json:"branch"`
	Repository   string     `json:"repository"`
	Image        string     `json:"image"`
	JobName      string     `json:"jobName"`
	TriggeredBy  string     `json:"triggeredBy"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	DurationMs   int        `json:"durationMs"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

type BuildFilter struct {
	AppID     string
	ProjectID string
	Status    string
	Limit     int
	Offset    int
}

func (d *DB) InsertBuild(ctx context.Context, b Build) (string, error) {
	var id string
	err := d.QueryRowContext(ctx,
		`INSERT INTO builds (app_id, project_id, environment, status, strategy, commit_sha, branch, repository, image, job_name, triggered_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		b.AppID, b.ProjectID, b.Environment, b.Status, b.Strategy, b.CommitSHA,
		b.Branch, b.Repository, b.Image, b.JobName, b.TriggeredBy,
	).Scan(&id)
	return id, err
}

func (d *DB) GetBuild(ctx context.Context, id string) (*Build, error) {
	var b Build
	var startedAt, finishedAt sql.NullTime
	err := d.QueryRowContext(ctx,
		`SELECT id, app_id, project_id, environment, status, strategy, commit_sha, branch, repository, image, job_name, triggered_by, started_at, finished_at, duration_ms, error_message, created_at
		 FROM builds WHERE id = $1`, id,
	).Scan(&b.ID, &b.AppID, &b.ProjectID, &b.Environment, &b.Status, &b.Strategy,
		&b.CommitSHA, &b.Branch, &b.Repository, &b.Image, &b.JobName, &b.TriggeredBy,
		&startedAt, &finishedAt, &b.DurationMs, &b.ErrorMessage, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	if startedAt.Valid {
		b.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		b.FinishedAt = &finishedAt.Time
	}
	return &b, nil
}

func (d *DB) UpdateBuildStatus(ctx context.Context, id, status string, errorMsg string) error {
	var query string
	switch status {
	case "running":
		query = `UPDATE builds SET status = $2, started_at = now() WHERE id = $1`
	case "success", "failed", "cancelled":
		query = `UPDATE builds SET status = $2, error_message = $3, finished_at = now(),
		         duration_ms = EXTRACT(EPOCH FROM (now() - COALESCE(started_at, created_at)))::int * 1000
		         WHERE id = $1`
	default:
		query = `UPDATE builds SET status = $2, error_message = $3 WHERE id = $1`
	}
	_, err := d.ExecContext(ctx, query, id, status, errorMsg)
	return err
}

func (d *DB) ListBuilds(ctx context.Context, f BuildFilter) ([]Build, int, error) {
	where := []string{}
	args := []interface{}{}
	idx := 1

	if f.AppID != "" {
		where = append(where, fmt.Sprintf("app_id = $%d", idx))
		args = append(args, f.AppID)
		idx++
	}
	if f.ProjectID != "" {
		where = append(where, fmt.Sprintf("project_id = $%d", idx))
		args = append(args, f.ProjectID)
		idx++
	}
	if f.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", idx))
		args = append(args, f.Status)
		idx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM builds "+whereClause, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count builds: %w", err)
	}

	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}

	query := fmt.Sprintf(
		`SELECT id, app_id, project_id, environment, status, strategy, commit_sha, branch, repository, image, job_name, triggered_by, started_at, finished_at, duration_ms, error_message, created_at
		 FROM builds %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, idx, idx+1,
	)
	args = append(args, f.Limit, f.Offset)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query builds: %w", err)
	}
	defer rows.Close()

	builds := []Build{}
	for rows.Next() {
		var b Build
		var startedAt, finishedAt sql.NullTime
		err := rows.Scan(&b.ID, &b.AppID, &b.ProjectID, &b.Environment, &b.Status, &b.Strategy,
			&b.CommitSHA, &b.Branch, &b.Repository, &b.Image, &b.JobName, &b.TriggeredBy,
			&startedAt, &finishedAt, &b.DurationMs, &b.ErrorMessage, &b.CreatedAt)
		if err != nil {
			return nil, 0, err
		}
		if startedAt.Valid {
			b.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			b.FinishedAt = &finishedAt.Time
		}
		builds = append(builds, b)
	}
	return builds, total, nil
}
