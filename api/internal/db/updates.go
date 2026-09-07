package db

import (
	"context"
	"time"
)

// UpdateRecord is one attempt to upgrade Vesta itself.
type UpdateRecord struct {
	ID          string     `json:"id"`
	FromVersion string     `json:"fromVersion"`
	ToVersion   string     `json:"toVersion"`
	JobName     string     `json:"jobName"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

func (d *DB) CreateUpdateRecord(ctx context.Context, from, to, jobName, triggeredBy string) (string, error) {
	var by interface{}
	if triggeredBy != "" {
		by = triggeredBy
	}
	var id string
	err := d.QueryRowContext(ctx, `
		INSERT INTO update_history (from_version, to_version, job_name, status, triggered_by)
		VALUES ($1, $2, $3, 'running', $4) RETURNING id`, from, to, jobName, by).Scan(&id)
	return id, err
}

// FinishUpdateRecord closes out an attempt.
//
// Matched on job_name rather than id because the process that finishes an upgrade is
// usually not the one that started it -- the upgrade restarts the API -- so the new
// process has only the Job to go on.
func (d *DB) FinishUpdateRecord(ctx context.Context, jobName, status, message string) error {
	_, err := d.ExecContext(ctx, `
		UPDATE update_history SET status = $2, message = $3, finished_at = now()
		WHERE job_name = $1 AND finished_at IS NULL`, jobName, status, message)
	return err
}

func (d *DB) ListUpdateHistory(ctx context.Context, limit int) ([]UpdateRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.QueryContext(ctx, `
		SELECT id, from_version, to_version, job_name, status, message, started_at, finished_at
		FROM update_history ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UpdateRecord, 0, limit)
	for rows.Next() {
		var r UpdateRecord
		if err := rows.Scan(&r.ID, &r.FromVersion, &r.ToVersion, &r.JobName,
			&r.Status, &r.Message, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunningUpdate returns the attempt still in flight, if any.
func (d *DB) RunningUpdate(ctx context.Context) (*UpdateRecord, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT id, from_version, to_version, job_name, status, message, started_at, finished_at
		FROM update_history WHERE finished_at IS NULL ORDER BY started_at DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	var r UpdateRecord
	if err := rows.Scan(&r.ID, &r.FromVersion, &r.ToVersion, &r.JobName,
		&r.Status, &r.Message, &r.StartedAt, &r.FinishedAt); err != nil {
		return nil, err
	}
	return &r, nil
}
