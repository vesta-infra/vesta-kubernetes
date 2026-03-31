package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type AuditLogEntry struct {
	ID           string                 `json:"id"`
	UserID       string                 `json:"userId"`
	Username     string                 `json:"username"`
	Action       string                 `json:"action"`
	ResourceType string                 `json:"resourceType"`
	ResourceID   string                 `json:"resourceId"`
	ResourceName string                 `json:"resourceName"`
	ProjectID    string                 `json:"projectId,omitempty"`
	Environment  string                 `json:"environment,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	IPAddress    string                 `json:"ipAddress,omitempty"`
	AuthMethod   string                 `json:"authMethod,omitempty"`
	CreatedAt    time.Time              `json:"createdAt"`
}

type AuditLogFilter struct {
	ProjectID    string
	AppID        string
	Action       string
	UserID       string
	ResourceType string
	From         *time.Time
	To           *time.Time
	Limit        int
	Offset       int
}

func (d *DB) InsertAuditLog(ctx context.Context, entry AuditLogEntry) error {
	metadata, _ := json.Marshal(entry.Metadata)
	if metadata == nil {
		metadata = []byte("{}")
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, username, action, resource_type, resource_id, resource_name, project_id, environment, metadata, ip_address, auth_method)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		entry.UserID, entry.Username, entry.Action, entry.ResourceType, entry.ResourceID,
		entry.ResourceName, entry.ProjectID, entry.Environment, metadata, entry.IPAddress, entry.AuthMethod,
	)
	return err
}

func (d *DB) ListAuditLogs(ctx context.Context, f AuditLogFilter) ([]AuditLogEntry, int, error) {
	where := []string{}
	args := []interface{}{}
	idx := 1

	if f.ProjectID != "" {
		where = append(where, fmt.Sprintf("project_id = $%d", idx))
		args = append(args, f.ProjectID)
		idx++
	}
	if f.AppID != "" {
		where = append(where, fmt.Sprintf("resource_id = $%d AND resource_type = 'app'", idx))
		args = append(args, f.AppID)
		idx++
	}
	if f.Action != "" {
		where = append(where, fmt.Sprintf("action = $%d", idx))
		args = append(args, f.Action)
		idx++
	}
	if f.UserID != "" {
		where = append(where, fmt.Sprintf("user_id = $%d", idx))
		args = append(args, f.UserID)
		idx++
	}
	if f.ResourceType != "" {
		where = append(where, fmt.Sprintf("resource_type = $%d", idx))
		args = append(args, f.ResourceType)
		idx++
	}
	if f.From != nil {
		where = append(where, fmt.Sprintf("created_at >= $%d", idx))
		args = append(args, *f.From)
		idx++
	}
	if f.To != nil {
		where = append(where, fmt.Sprintf("created_at <= $%d", idx))
		args = append(args, *f.To)
		idx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count total
	var total int
	countQuery := "SELECT COUNT(*) FROM audit_log " + whereClause
	err := d.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}

	query := fmt.Sprintf(
		`SELECT id, user_id, username, action, resource_type, resource_id, resource_name, project_id, environment, metadata, ip_address, auth_method, created_at
		 FROM audit_log %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, idx, idx+1,
	)
	args = append(args, f.Limit, f.Offset)

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query audit logs: %w", err)
	}
	defer rows.Close()

	entries := []AuditLogEntry{}
	for rows.Next() {
		var e AuditLogEntry
		var metaJSON []byte
		var ipAddr, authMethod sql.NullString
		err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.Action, &e.ResourceType, &e.ResourceID,
			&e.ResourceName, &e.ProjectID, &e.Environment, &metaJSON, &ipAddr, &authMethod, &e.CreatedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		if len(metaJSON) > 0 {
			json.Unmarshal(metaJSON, &e.Metadata)
		}
		if ipAddr.Valid {
			e.IPAddress = ipAddr.String
		}
		if authMethod.Valid {
			e.AuthMethod = authMethod.String
		}
		entries = append(entries, e)
	}

	return entries, total, nil
}
