package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type WebhookDelivery struct {
	ID               string                 `json:"id"`
	Provider         string                 `json:"provider"`
	EventType        string                 `json:"eventType"`
	DeliveryID       string                 `json:"deliveryId"`
	Repository       string                 `json:"repository"`
	Branch           string                 `json:"branch"`
	CommitSHA        string                 `json:"commitSha"`
	Payload          map[string]interface{} `json:"payload,omitempty"`
	Status           string                 `json:"status"`
	ProcessingResult string                 `json:"processingResult"`
	AppsTriggered    []string               `json:"appsTriggered"`
	DurationMs       int                    `json:"durationMs"`
	IPAddress        string                 `json:"ipAddress"`
	CreatedAt        time.Time              `json:"createdAt"`
}

type WebhookDeliveryFilter struct {
	Provider   string
	Status     string
	Repository string
	Limit      int
	Offset     int
}

func (d *DB) InsertWebhookDelivery(ctx context.Context, entry WebhookDelivery) (string, error) {
	payload, _ := json.Marshal(entry.Payload)
	if payload == nil {
		payload = []byte("{}")
	}
	var id string
	err := d.QueryRowContext(ctx,
		`INSERT INTO webhook_deliveries (provider, event_type, delivery_id, repository, branch, commit_sha, payload, status, processing_result, apps_triggered, duration_ms, ip_address)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
		entry.Provider, entry.EventType, entry.DeliveryID, entry.Repository, entry.Branch, entry.CommitSHA,
		payload, entry.Status, entry.ProcessingResult, pq.Array(entry.AppsTriggered),
		entry.DurationMs, entry.IPAddress,
	).Scan(&id)
	return id, err
}

func (d *DB) UpdateWebhookDelivery(ctx context.Context, id string, status string, result string, apps []string, durationMs int) error {
	_, err := d.ExecContext(ctx,
		`UPDATE webhook_deliveries SET status = $2, processing_result = $3, apps_triggered = $4, duration_ms = $5 WHERE id = $1`,
		id, status, result, pq.Array(apps), durationMs,
	)
	return err
}

func (d *DB) ListWebhookDeliveries(ctx context.Context, f WebhookDeliveryFilter) ([]WebhookDelivery, int, error) {
	where := []string{}
	args := []interface{}{}
	idx := 1

	if f.Provider != "" {
		where = append(where, fmt.Sprintf("provider = $%d", idx))
		args = append(args, f.Provider)
		idx++
	}
	if f.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", idx))
		args = append(args, f.Status)
		idx++
	}
	if f.Repository != "" {
		where = append(where, fmt.Sprintf("repository ILIKE $%d", idx))
		args = append(args, "%"+f.Repository+"%")
		idx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int
	err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_deliveries "+whereClause, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count webhook deliveries: %w", err)
	}

	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}

	query := fmt.Sprintf(
		`SELECT id, provider, event_type, delivery_id, repository, branch, commit_sha, payload, status, processing_result, apps_triggered, duration_ms, ip_address, created_at
		 FROM webhook_deliveries %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, idx, idx+1,
	)
	args = append(args, f.Limit, f.Offset)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query webhook deliveries: %w", err)
	}
	defer rows.Close()

	entries := []WebhookDelivery{}
	for rows.Next() {
		var e WebhookDelivery
		var payloadJSON []byte
		var ipAddr sql.NullString
		err := rows.Scan(&e.ID, &e.Provider, &e.EventType, &e.DeliveryID, &e.Repository, &e.Branch,
			&e.CommitSHA, &payloadJSON, &e.Status, &e.ProcessingResult, pq.Array(&e.AppsTriggered),
			&e.DurationMs, &ipAddr, &e.CreatedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("scan webhook delivery: %w", err)
		}
		if len(payloadJSON) > 0 {
			json.Unmarshal(payloadJSON, &e.Payload)
		}
		if ipAddr.Valid {
			e.IPAddress = ipAddr.String
		}
		entries = append(entries, e)
	}

	return entries, total, nil
}
