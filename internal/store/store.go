package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

type Store struct{ db *sql.DB }

var ErrNotFound = sql.ErrNoRows

type Group struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Order  int         `json:"order"`
	Items  []Component `json:"components,omitempty"`
}

type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Link        string `json:"link,omitempty"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Order       int    `json:"order"`
	GroupID     string `json:"groupId,omitempty"`
}

type StatusChange struct {
	ID            int64  `json:"id"`
	ComponentID   string `json:"componentId"`
	ComponentName string `json:"componentName"`
	OldStatus     string `json:"oldStatus"`
	NewStatus     string `json:"newStatus"`
	Message       string `json:"message"`
	ChangedAt     string `json:"changedAt"`
}

type SeedGroup struct {
	Name       string        `json:"name"`
	Components []SeedService `json:"components"`
}

type SeedService struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Link           string `json:"link"`
	ComponentGroup string `json:"componentGroup"`
}

type SeedConfig struct {
	PageTitle     string        `json:"pageTitle"`
	LogoURL       string        `json:"logoUrl"`
	Intro         string        `json:"intro"`
	SupportEmail  string        `json:"supportEmail"`
	PrivacyURL    string        `json:"privacyUrl"`
	ImprintURL    string        `json:"imprintUrl"`
	Groups        []SeedGroup   `json:"groups"`
	Components    []SeedService `json:"components"`
	AdminUser     string        `json:"adminUser"`
	AdminPassword string        `json:"adminPassword"`
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	const migration = `
CREATE TABLE IF NOT EXISTS app_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS status_groups (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS status_components (
  id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL REFERENCES status_groups(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'operational' CHECK (status IN ('operational','degraded','partial_outage','major_outage','maintenance')),
  message TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS admin_users (
  username TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin','operator')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS status_changes (
  id BIGSERIAL PRIMARY KEY,
  component_id TEXT NOT NULL REFERENCES status_components(id) ON DELETE CASCADE,
  old_status TEXT NOT NULL,
  new_status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  changed_by TEXT NOT NULL,
  changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS status_changes_component_time ON status_changes(component_id, changed_at DESC);
`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(827364,1)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=1)`).Scan(&applied); err != nil {
		return err
	}
	if !applied {
		for _, statement := range strings.Split(migration, ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(1)`); err != nil {
			return err
		}
	}
	var tokenRemovalApplied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=2)`).Scan(&tokenRemovalApplied); err != nil {
		return err
	}
	if !tokenRemovalApplied {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS api_tokens`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(2)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func SeedConfigFromEnv() (SeedConfig, error) {
	var cfg SeedConfig
	if raw := strings.TrimSpace(os.Getenv("STATUS_INITIAL_CONFIG")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return SeedConfig{}, fmt.Errorf("parse STATUS_INITIAL_CONFIG: %w", err)
		}
	}
	if cfg.AdminUser == "" {
		cfg.AdminUser = strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_USER"))
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	}
	return cfg, nil
}

func (s *Store) Seed(ctx context.Context, cfg SeedConfig) error {
	if cfg.AdminUser == "" || cfg.AdminPassword == "" {
		return errors.New("BOOTSTRAP_ADMIN_USER and BOOTSTRAP_ADMIN_PASSWORD are required")
	}
	if len(cfg.AdminPassword) < 12 {
		return errors.New("bootstrap administrator password must be at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO admin_users(username,password_hash,role) VALUES($1,$2,'admin') ON CONFLICT(username) DO NOTHING`, cfg.AdminUser, string(hash)); err != nil {
		return err
	}

	settings := map[string]string{
		"page_title":    cfg.PageTitle,
		"logo_url":      cfg.LogoURL,
		"intro":         cfg.Intro,
		"support_email": cfg.SupportEmail,
		"privacy_url":   cfg.PrivacyURL,
		"imprint_url":   cfg.ImprintURL,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range settings {
		if value != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, value); err != nil {
				return err
			}
		}
	}
	for groupIndex, group := range cfg.Groups {
		groupID := slug(group.Name)
		if groupID == "" {
			return fmt.Errorf("group at position %d has no valid name", groupIndex)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO status_groups(id,name,position) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,position=EXCLUDED.position`, groupID, group.Name, groupIndex); err != nil {
			return err
		}
		for itemIndex, item := range group.Components {
			id := item.ID
			if id == "" {
				id = slug(item.Name)
			}
			if id == "" {
				return fmt.Errorf("component at position %d in %q has no valid name", itemIndex, group.Name)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO status_components(id,group_id,name,description,link,position) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET group_id=EXCLUDED.group_id,name=EXCLUDED.name,description=EXCLUDED.description,link=EXCLUDED.link,position=EXCLUDED.position`, id, groupID, item.Name, item.Description, item.Link, itemIndex); err != nil {
				return err
			}
		}
	}
	for itemIndex, item := range cfg.Components {
		id := item.ID
		if id == "" {
			id = slug(item.Name)
		}
		groupID := slug(item.ComponentGroup)
		if id == "" || groupID == "" {
			return fmt.Errorf("component at position %d needs a valid name and componentGroup", itemIndex)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO status_components(id,group_id,name,description,link,position) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET group_id=EXCLUDED.group_id,name=EXCLUDED.name,description=EXCLUDED.description,link=EXCLUDED.link,position=EXCLUDED.position`, id, groupID, item.Name, item.Description, item.Link, itemIndex); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func slug(s string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
			lastDash = false
		} else if !lastDash && out.Len() > 0 {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func (s *Store) PublicData(ctx context.Context) (map[string]string, []Group, error) {
	settings := map[string]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM app_settings`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, nil, err
		}
		settings[k] = v
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	groups, err := s.db.QueryContext(ctx, `SELECT id,name,position FROM status_groups ORDER BY position,name`)
	if err != nil {
		return nil, nil, err
	}
	defer groups.Close()
	var result []Group
	for groups.Next() {
		var g Group
		if err := groups.Scan(&g.ID, &g.Name, &g.Order); err != nil {
			return nil, nil, err
		}
		result = append(result, g)
	}
	if err := groups.Err(); err != nil {
		return nil, nil, err
	}
	for i := range result {
		rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,link,status,message,updated_at,position,group_id FROM status_components WHERE group_id=$1 ORDER BY position,name`, result[i].ID)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var c Component
			var at time.Time
			if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Link, &c.Status, &c.Message, &at, &c.Order, &c.GroupID); err != nil {
				rows.Close()
				return nil, nil, err
			}
			c.UpdatedAt = at.UTC().Format(time.RFC3339)
			result[i].Items = append(result[i].Items, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rows.Close()
		result[i].Status = aggregate(result[i].Items)
	}
	return settings, result, nil
}

func aggregate(items []Component) string {
	status := "operational"
	for _, component := range items {
		switch component.Status {
		case "major_outage":
			return "major_outage"
		case "partial_outage":
			status = "partial_outage"
		case "degraded":
			if status == "operational" {
				status = "degraded"
			}
		case "maintenance":
			if status == "operational" {
				status = "maintenance"
			}
		}
	}
	return status
}

func (s *Store) SetStatus(ctx context.Context, id, status, message, actor string) error {
	if !validStatus(status) {
		return fmt.Errorf("unsupported status %q", status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM status_components WHERE id=$1 FOR UPDATE`, id).Scan(&old); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE status_components SET status=$2,message=$3,updated_at=now() WHERE id=$1`, id, status, message); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO status_changes(component_id,old_status,new_status,message,changed_by) VALUES($1,$2,$3,$4,$5)`, id, old, status, message, actor); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StatusHistory(ctx context.Context, limit int) ([]StatusChange, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT sc.id, sc.component_id, c.name, sc.old_status, sc.new_status, sc.message, sc.changed_at
FROM status_changes sc
JOIN status_components c ON c.id=sc.component_id
ORDER BY sc.changed_at DESC, sc.id DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []StatusChange
	for rows.Next() {
		var change StatusChange
		var changedAt time.Time
		if err := rows.Scan(&change.ID, &change.ComponentID, &change.ComponentName, &change.OldStatus, &change.NewStatus, &change.Message, &changedAt); err != nil {
			return nil, err
		}
		change.ChangedAt = changedAt.UTC().Format(time.RFC3339)
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func (s *Store) VerifyUser(ctx context.Context, username, password string) (string, bool) {
	var hash, role string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash,role FROM admin_users WHERE username=$1`, username).Scan(&hash, &role)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", false
	}
	return role, true
}

func (s *Store) CreateUser(ctx context.Context, username, password, role string) error {
	if username == "" || len(password) < 12 || (role != "admin" && role != "operator") {
		return errors.New("invalid username, password, or role")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO admin_users(username,password_hash,role) VALUES($1,$2,$3)`, username, string(hash), role)
	return err
}

func (s *Store) ChangePassword(ctx context.Context, username, currentPassword, newPassword string) error {
	if len(newPassword) < 12 {
		return errors.New("new password must be at least 12 characters")
	}
	var currentHash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM admin_users WHERE username=$1`, username).Scan(&currentHash); err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(currentPassword)); err != nil {
		return errors.New("current password is incorrect")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE admin_users SET password_hash=$2 WHERE username=$1`, username, string(newHash))
	return err
}

func validStatus(s string) bool {
	switch s {
	case "operational", "degraded", "partial_outage", "major_outage", "maintenance":
		return true
	}
	return false
}
