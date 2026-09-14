package db

import (
	"context"
	"strconv"
	"time"
)

// CostSample is one observation of what a workload was reserving.
type CostSample struct {
	ProjectID   string
	Environment string
	AppID       string
	Kind        string
	Replicas    int32

	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64

	CPUUsedMillicores int64
	MemoryUsedBytes   int64

	Interval time.Duration
}

// CostRollup is what a period cost, grouped.
type CostRollup struct {
	Key         string `json:"key"`
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment,omitempty"`
	AppID       string `json:"appId,omitempty"`
	Kind        string `json:"kind,omitempty"`

	// Weighted by how long each sample stood for, so a workload that was resized partway
	// through a window is charged for what it actually held at each point.
	CPUCoreHours    float64 `json:"cpuCoreHours"`
	MemoryGiBHours  float64 `json:"memoryGiBHours"`
	StorageGiBHours float64 `json:"storageGiBHours"`

	CPUUsedCoreHours   float64 `json:"cpuUsedCoreHours"`
	MemoryUsedGiBHours float64 `json:"memoryUsedGiBHours"`
}

// InsertCostSamples records a batch of observations.
func (d *DB) InsertCostSamples(ctx context.Context, samples []CostSample) error {
	if len(samples) == 0 {
		return nil
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO cost_samples
			(project_id, environment, app_id, kind, replicas,
			 cpu_millicores, memory_bytes, storage_bytes,
			 cpu_used_millicores, memory_used_bytes, interval_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range samples {
		if _, err := stmt.ExecContext(ctx,
			s.ProjectID, s.Environment, s.AppID, s.Kind, s.Replicas,
			s.CPUMillicores, s.MemoryBytes, s.StorageBytes,
			s.CPUUsedMillicores, s.MemoryUsedBytes, int(s.Interval.Seconds()),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CostRollups sums reservations over a window.
//
// groupBy is "app" or "environment". The aggregation happens in Postgres rather than in Go
// because a month of five-minute samples for a busy instance is a lot of rows to move just
// to add them up.
func (d *DB) CostRollups(ctx context.Context, projectID, appID, groupBy string, since time.Time) ([]CostRollup, error) {
	groupColumn := "app_id"
	if groupBy == "environment" {
		groupColumn = "environment"
	}

	query := `
		SELECT ` + groupColumn + ` AS key,
		       project_id,
		       max(environment), max(app_id), max(kind),
		       -- Each sample weighted by the interval it stands for, so a gap in sampling
		       -- is not charged as continuous running at the last observed size.
		       sum(replicas * cpu_millicores  * interval_seconds) / 1000.0 / 3600.0,
		       sum(replicas * memory_bytes    * interval_seconds) / 1073741824.0 / 3600.0,
		       sum(storage_bytes              * interval_seconds) / 1073741824.0 / 3600.0,
		       sum(cpu_used_millicores        * interval_seconds) / 1000.0 / 3600.0,
		       sum(memory_used_bytes          * interval_seconds) / 1073741824.0 / 3600.0
		FROM cost_samples
		WHERE sampled_at >= $1`

	args := []any{since}
	if projectID != "" {
		args = append(args, projectID)
		query += ` AND project_id = $2`
	}
	if appID != "" {
		args = append(args, appID)
		query += ` AND app_id = $` + strconv.Itoa(len(args))
	}
	query += ` GROUP BY ` + groupColumn + `, project_id ORDER BY 6 DESC`

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CostRollup{}
	for rows.Next() {
		var r CostRollup
		if err := rows.Scan(&r.Key, &r.ProjectID, &r.Environment, &r.AppID, &r.Kind,
			&r.CPUCoreHours, &r.MemoryGiBHours, &r.StorageGiBHours,
			&r.CPUUsedCoreHours, &r.MemoryUsedGiBHours); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneCostSamples drops observations older than the retention window.
//
// Without this the table grows without bound: roughly 8,600 rows per app-environment per
// month at a five-minute interval, which is fine for a while and then quietly is not.
func (d *DB) PruneCostSamples(ctx context.Context, keepFor time.Duration) error {
	_, err := d.ExecContext(ctx,
		`DELETE FROM cost_samples WHERE sampled_at < now() - $1::interval`, keepFor.String())
	return err
}
