package db

import (
	"context"
	"time"
)

type AlertRule struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"projectId"`
	AppID           string     `json:"appId"`
	Name            string     `json:"name"`
	Metric          string     `json:"metric"`
	Operator        string     `json:"operator"`
	Threshold       float64    `json:"threshold"`
	DurationSeconds int        `json:"durationSeconds"`
	Environment     string     `json:"environment"`
	Enabled         bool       `json:"enabled"`
	LastTriggeredAt *time.Time `json:"lastTriggeredAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

func (d *DB) CreateAlertRule(ctx context.Context, rule *AlertRule) (*AlertRule, error) {
	r := &AlertRule{}
	err := d.QueryRowContext(ctx,
		`INSERT INTO alert_rules (project_id, app_id, name, metric, operator, threshold, duration_seconds, environment, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, project_id, app_id, name, metric, operator, threshold, duration_seconds, environment, enabled, last_triggered_at, created_at`,
		rule.ProjectID, rule.AppID, rule.Name, rule.Metric, rule.Operator, rule.Threshold, rule.DurationSeconds, rule.Environment, rule.Enabled,
	).Scan(&r.ID, &r.ProjectID, &r.AppID, &r.Name, &r.Metric, &r.Operator, &r.Threshold, &r.DurationSeconds, &r.Environment, &r.Enabled, &r.LastTriggeredAt, &r.CreatedAt)
	return r, err
}

func (d *DB) ListAlertRules(ctx context.Context, projectID string) ([]AlertRule, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, project_id, app_id, name, metric, operator, threshold, duration_seconds, environment, enabled, last_triggered_at, created_at
		 FROM alert_rules WHERE project_id = $1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []AlertRule
	for rows.Next() {
		var r AlertRule
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.AppID, &r.Name, &r.Metric, &r.Operator, &r.Threshold, &r.DurationSeconds, &r.Environment, &r.Enabled, &r.LastTriggeredAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (d *DB) UpdateAlertRule(ctx context.Context, id string, enabled bool) error {
	_, err := d.ExecContext(ctx,
		`UPDATE alert_rules SET enabled = $2 WHERE id = $1`, id, enabled)
	return err
}

func (d *DB) DeleteAlertRule(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	return err
}

func (d *DB) MarkAlertTriggered(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE alert_rules SET last_triggered_at = now() WHERE id = $1`, id)
	return err
}
