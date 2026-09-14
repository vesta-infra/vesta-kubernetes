package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// GitConnection is one configured link to a git server.
//
// The credential is not here. SecretName points at a Kubernetes Secret holding it, so this
// row can be selected, logged and serialised to the UI without a token coming with it.
type GitConnection struct {
	ID          string            `json:"id"`
	Provider    string            `json:"provider"`
	DisplayName string            `json:"displayName"`
	BaseURL     string            `json:"baseUrl,omitempty"`
	Host        string            `json:"host"`
	Account     string            `json:"account,omitempty"`
	SecretName  string            `json:"-"`
	ExternalID  string            `json:"externalId,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	IsLegacy    bool              `json:"isLegacy,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

const gitConnectionColumns = `id, provider, display_name, base_url, host, account,
	secret_name, external_id, metadata, is_legacy, created_at, updated_at`

func scanGitConnection(row interface{ Scan(...any) error }) (GitConnection, error) {
	var c GitConnection
	var meta []byte
	err := row.Scan(&c.ID, &c.Provider, &c.DisplayName, &c.BaseURL, &c.Host, &c.Account,
		&c.SecretName, &c.ExternalID, &meta, &c.IsLegacy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if len(meta) > 0 {
		// A metadata column that will not decode is not worth failing a webhook over --
		// it holds display hints like the App slug, nothing load-bearing.
		_ = json.Unmarshal(meta, &c.Metadata)
	}
	return c, nil
}

// CreateGitConnection inserts a connection and returns it with its generated id.
func (d *DB) CreateGitConnection(ctx context.Context, c GitConnection) (GitConnection, error) {
	meta, err := json.Marshal(c.Metadata)
	if err != nil {
		return GitConnection{}, err
	}
	if c.Metadata == nil {
		meta = []byte("{}")
	}

	// An explicit id is allowed because the App-manifest flow has to know the connection's
	// id before the App exists: the webhook URL baked into the manifest contains it, and
	// that URL cannot be changed afterwards without editing the App on GitHub.
	row := d.QueryRowContext(ctx, `
		INSERT INTO git_connections
			(id, provider, display_name, base_url, host, account, secret_name, external_id, metadata, is_legacy)
		VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+gitConnectionColumns,
		c.ID, c.Provider, c.DisplayName, c.BaseURL, c.Host, c.Account, c.SecretName,
		c.ExternalID, meta, c.IsLegacy)

	out, err := scanGitConnection(row)
	if isUniqueViolation(err) {
		return GitConnection{}, ErrDuplicate
	}
	return out, err
}

func (d *DB) GetGitConnection(ctx context.Context, id string) (GitConnection, error) {
	row := d.QueryRowContext(ctx,
		`SELECT `+gitConnectionColumns+` FROM git_connections WHERE id = $1`, id)
	c, err := scanGitConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GitConnection{}, ErrNotFound
	}
	return c, err
}

// ListGitConnections returns every connection, newest first. Pass an empty provider for all.
func (d *DB) ListGitConnections(ctx context.Context, provider string) ([]GitConnection, error) {
	query := `SELECT ` + gitConnectionColumns + ` FROM git_connections`
	args := []any{}
	if provider != "" {
		query += ` WHERE provider = $1`
		args = append(args, provider)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GitConnection{}
	for rows.Next() {
		c, err := scanGitConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FindGitConnectionForRepo returns the connection that serves a repository.
//
// Matching is on provider and host, not on the repository path: a connection grants access
// to a set of repositories that changes without Vesta being told, so the host is the only
// stable thing to key on. Account narrows it when two connections point at the same server
// for different organisations; the connection whose account prefixes the repository path
// wins, and a connection with no account is the fallback.
func (d *DB) FindGitConnectionForRepo(ctx context.Context, provider, host, owner string) (GitConnection, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT `+gitConnectionColumns+`
		FROM git_connections
		WHERE provider = $1 AND host = $2
		ORDER BY (account = $3) DESC, (account = '') ASC, created_at ASC`,
		provider, host, owner)
	if err != nil {
		return GitConnection{}, err
	}
	defer rows.Close()

	if !rows.Next() {
		return GitConnection{}, ErrNotFound
	}
	c, err := scanGitConnection(rows)
	if err != nil {
		return GitConnection{}, err
	}
	return c, rows.Err()
}

// GetLegacyGitConnection returns the connection adopted from the pre-connections GitHub
// App, which is where a webhook created before this release resolves to.
func (d *DB) GetLegacyGitConnection(ctx context.Context, provider string) (GitConnection, error) {
	row := d.QueryRowContext(ctx,
		`SELECT `+gitConnectionColumns+`
		 FROM git_connections WHERE provider = $1 AND is_legacy = true
		 ORDER BY created_at ASC LIMIT 1`, provider)
	c, err := scanGitConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GitConnection{}, ErrNotFound
	}
	return c, err
}

func (d *DB) DeleteGitConnection(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM git_connections WHERE id = $1`, id)
	return err
}

// CountGitConnectionsBySecret reports how many connections reference a Secret, so deleting
// one does not remove credentials another still needs.
func (d *DB) CountGitConnectionsBySecret(ctx context.Context, secretName string) (int, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT count(*) FROM git_connections WHERE secret_name = $1`, secretName).Scan(&n)
	return n, err
}

// --- OAuth handshake state ---

// PutOAuthState records a pending handshake. draft carries whatever the callback needs to
// finish, such as the UI base URL to return to.
func (d *DB) PutOAuthState(ctx context.Context, state, provider string, draft map[string]string, ttl time.Duration) error {
	payload, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	// Expired rows are cleared on the way in rather than by a background sweeper. The
	// table only grows when someone starts a connection and never finishes, so the write
	// path is exactly where the tidying belongs and there is no worker to forget to start.
	if _, err := d.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at <= now()`); err != nil {
		// Tidying is not worth failing a connection attempt over.
		_ = err
	}

	_, err = d.ExecContext(ctx, `
		INSERT INTO oauth_states (state, provider, draft, expires_at)
		VALUES ($1, $2, $3, now() + $4::interval)`,
		state, provider, payload, ttl.String())
	return err
}

// TakeOAuthState consumes a state, returning ErrNotFound if it is unknown or expired.
//
// Deleting as part of the read is what makes it single-use: a state that could be replayed
// would let a callback be repeated.
func (d *DB) TakeOAuthState(ctx context.Context, state string) (string, map[string]string, error) {
	var provider string
	var draft []byte

	err := d.QueryRowContext(ctx, `
		DELETE FROM oauth_states
		WHERE state = $1 AND expires_at > now()
		RETURNING provider, draft`, state).Scan(&provider, &draft)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}

	out := map[string]string{}
	_ = json.Unmarshal(draft, &out)
	return provider, out, nil
}
