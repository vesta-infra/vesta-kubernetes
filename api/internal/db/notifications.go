package db

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

type NotificationChannel struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	Events    []string        `json:"events"`
	Enabled   bool            `json:"enabled"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type NotificationHistory struct {
	ID          string          `json:"id"`
	ChannelID   string          `json:"channelId"`
	ProjectID   string          `json:"projectId"`
	EventType   string          `json:"eventType"`
	AppID       string          `json:"appId"`
	Environment string          `json:"environment"`
	Status      string          `json:"status"`
	Payload     json.RawMessage `json:"payload"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

func (d *DB) CreateNotificationChannel(ch *NotificationChannel) error {
	return d.QueryRow(
		`INSERT INTO notification_channels (project_id, name, type, config, events, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at, updated_at`,
		ch.ProjectID, ch.Name, ch.Type, ch.Config, pq.Array(ch.Events), ch.Enabled,
	).Scan(&ch.ID, &ch.CreatedAt, &ch.UpdatedAt)
}

func (d *DB) ListNotificationChannels(projectID string) ([]NotificationChannel, error) {
	rows, err := d.Query(
		`SELECT id, project_id, name, type, config, events, enabled, created_at, updated_at
		 FROM notification_channels WHERE project_id = $1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []NotificationChannel
	for rows.Next() {
		var ch NotificationChannel
		if err := rows.Scan(&ch.ID, &ch.ProjectID, &ch.Name, &ch.Type, &ch.Config,
			pq.Array(&ch.Events), &ch.Enabled, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (d *DB) GetNotificationChannel(id string) (*NotificationChannel, error) {
	var ch NotificationChannel
	err := d.QueryRow(
		`SELECT id, project_id, name, type, config, events, enabled, created_at, updated_at
		 FROM notification_channels WHERE id = $1`, id,
	).Scan(&ch.ID, &ch.ProjectID, &ch.Name, &ch.Type, &ch.Config,
		pq.Array(&ch.Events), &ch.Enabled, &ch.CreatedAt, &ch.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (d *DB) UpdateNotificationChannel(ch *NotificationChannel) error {
	_, err := d.Exec(
		`UPDATE notification_channels
		 SET name = $1, config = $2, events = $3, enabled = $4, updated_at = now()
		 WHERE id = $5`,
		ch.Name, ch.Config, pq.Array(ch.Events), ch.Enabled, ch.ID)
	return err
}

func (d *DB) DeleteNotificationChannel(id string) error {
	_, err := d.Exec(`DELETE FROM notification_channels WHERE id = $1`, id)
	return err
}

func (d *DB) GetChannelsForEvent(projectID, eventType string) ([]NotificationChannel, error) {
	rows, err := d.Query(
		`SELECT id, project_id, name, type, config, events, enabled, created_at, updated_at
		 FROM notification_channels
		 WHERE project_id = $1 AND enabled = true AND $2 = ANY(events)
		 ORDER BY created_at`, projectID, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []NotificationChannel
	for rows.Next() {
		var ch NotificationChannel
		if err := rows.Scan(&ch.ID, &ch.ProjectID, &ch.Name, &ch.Type, &ch.Config,
			pq.Array(&ch.Events), &ch.Enabled, &ch.CreatedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (d *DB) InsertNotificationHistory(h *NotificationHistory) error {
	return d.QueryRow(
		`INSERT INTO notification_history (channel_id, project_id, event_type, app_id, environment, status, payload, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, created_at`,
		h.ChannelID, h.ProjectID, h.EventType, h.AppID, h.Environment, h.Status, h.Payload, h.Error,
	).Scan(&h.ID, &h.CreatedAt)
}

func (d *DB) ListNotificationHistory(projectID string, limit int) ([]NotificationHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := d.Query(
		`SELECT h.id, h.channel_id, h.project_id, h.event_type, h.app_id, h.environment,
		        h.status, h.payload, h.error, h.created_at
		 FROM notification_history h
		 WHERE h.project_id = $1
		 ORDER BY h.created_at DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []NotificationHistory
	for rows.Next() {
		var item NotificationHistory
		if err := rows.Scan(&item.ID, &item.ChannelID, &item.ProjectID, &item.EventType,
			&item.AppID, &item.Environment, &item.Status, &item.Payload, &item.Error, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetAnyEmailChannel returns the first enabled email notification channel across all projects.
// Used for system-level emails like password resets.
func (d *DB) GetAnyEmailChannel() (*NotificationChannel, error) {
	var ch NotificationChannel
	err := d.QueryRow(
		`SELECT id, project_id, name, type, config, events, enabled, created_at, updated_at
		 FROM notification_channels
		 WHERE type = 'email' AND enabled = true
		 ORDER BY created_at LIMIT 1`,
	).Scan(&ch.ID, &ch.ProjectID, &ch.Name, &ch.Type, &ch.Config,
		pq.Array(&ch.Events), &ch.Enabled, &ch.CreatedAt, &ch.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// HasEmailChannel reports whether any enabled email notification channel exists.
func (d *DB) HasEmailChannel() (bool, error) {
	var exists bool
	err := d.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM notification_channels WHERE type = 'email' AND enabled = true)`,
	).Scan(&exists)
	return exists, err
}
