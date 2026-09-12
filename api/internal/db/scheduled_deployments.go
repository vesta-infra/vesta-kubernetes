package db

import (
	"context"
	"time"
)

type ScheduledDeployment struct {
	ID            string    `json:"id"`
	AppID         string    `json:"appId"`
	ProjectID     string    `json:"projectId"`
	Environment   string    `json:"environment"`
	Image         string    `json:"image"`
	Tag           string    `json:"tag"`
	ScheduledAt   time.Time `json:"scheduledAt"`
	Status        string    `json:"status"`
	ResultMessage string    `json:"resultMessage"`
	CreatedBy     string    `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
}

func (d *DB) CreateScheduledDeployment(ctx context.Context, sd *ScheduledDeployment) error {
	return d.QueryRowContext(ctx,
		`INSERT INTO scheduled_deployments (app_id, project_id, environment, image, tag, scheduled_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		sd.AppID, sd.ProjectID, sd.Environment, sd.Image, sd.Tag, sd.ScheduledAt, sd.CreatedBy,
	).Scan(&sd.ID, &sd.CreatedAt)
}

func (d *DB) ListScheduledDeployments(ctx context.Context, projectID string) ([]ScheduledDeployment, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, app_id, project_id, environment, image, tag, scheduled_at, status, result_message, created_by, created_at
		FROM scheduled_deployments WHERE project_id = $1 ORDER BY scheduled_at DESC LIMIT 50`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ScheduledDeployment
	for rows.Next() {
		var sd ScheduledDeployment
		if err := rows.Scan(&sd.ID, &sd.AppID, &sd.ProjectID, &sd.Environment, &sd.Image, &sd.Tag,
			&sd.ScheduledAt, &sd.Status, &sd.ResultMessage, &sd.CreatedBy, &sd.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, sd)
	}
	return items, nil
}

func (d *DB) GetPendingScheduledDeployments(ctx context.Context) ([]ScheduledDeployment, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, app_id, project_id, environment, image, tag, scheduled_at, status, result_message, created_by, created_at
		FROM scheduled_deployments WHERE status = 'pending' AND scheduled_at <= NOW()
		ORDER BY scheduled_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ScheduledDeployment
	for rows.Next() {
		var sd ScheduledDeployment
		if err := rows.Scan(&sd.ID, &sd.AppID, &sd.ProjectID, &sd.Environment, &sd.Image, &sd.Tag,
			&sd.ScheduledAt, &sd.Status, &sd.ResultMessage, &sd.CreatedBy, &sd.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, sd)
	}
	return items, nil
}

func (d *DB) UpdateScheduledDeploymentStatus(ctx context.Context, id, status, message string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE scheduled_deployments SET status = $1, result_message = $2 WHERE id = $3`,
		status, message, id)
	return err
}

func (d *DB) CancelScheduledDeployment(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE scheduled_deployments SET status = 'cancelled' WHERE id = $1 AND status = 'pending'`, id)
	return err
}
