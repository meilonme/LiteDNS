package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var schemaStatements = []string{
	`PRAGMA foreign_keys = ON;`,
	`CREATE TABLE IF NOT EXISTS admins (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		must_change_password INTEGER NOT NULL DEFAULT 1,
		last_login_at DATETIME NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		admin_id INTEGER NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		expires_at DATETIME NOT NULL,
		revoked_at DATETIME NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY (admin_id) REFERENCES admins(id) ON DELETE CASCADE
	);`,
	`CREATE TABLE IF NOT EXISTS vendors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		provider TEXT NOT NULL CHECK (provider IN ('aliyun', 'cloudflare')),
		api_key TEXT NOT NULL,
		api_secret_cipher TEXT NOT NULL,
		extra_json TEXT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		vendor_id INTEGER NOT NULL,
		remote_domain_id TEXT NULL,
		domain_name TEXT NOT NULL,
		last_synced_at DATETIME NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(vendor_id, domain_name),
		FOREIGN KEY (vendor_id) REFERENCES vendors(id) ON DELETE RESTRICT
	);`,
	`CREATE TABLE IF NOT EXISTS records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL,
		remote_record_id TEXT NOT NULL,
		host TEXT NOT NULL,
		type TEXT NOT NULL,
		value TEXT NOT NULL,
		ttl INTEGER NOT NULL,
		proxied INTEGER NOT NULL DEFAULT 0,
		line TEXT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(domain_id, host, type),
		FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE
	);`,
	`CREATE TABLE IF NOT EXISTS ddns_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL,
		host TEXT NOT NULL,
		record_type TEXT NOT NULL CHECK (record_type IN ('A', 'AAAA')),
		interval_sec INTEGER NOT NULL DEFAULT 300,
		status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'paused')),
		last_ip TEXT NULL,
		last_check_at DATETIME NULL,
		last_success_at DATETIME NULL,
		consecutive_failures INTEGER NOT NULL DEFAULT 0,
		next_run_at DATETIME NOT NULL,
		last_error TEXT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(domain_id, host, record_type),
		FOREIGN KEY (domain_id) REFERENCES domains(id) ON DELETE CASCADE
	);`,
	`CREATE TABLE IF NOT EXISTS ddns_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		old_ip TEXT NULL,
		new_ip TEXT NULL,
		action TEXT NOT NULL CHECK (action IN ('skip', 'update', 'create')),
		result TEXT NOT NULL CHECK (result IN ('success', 'failed')),
		error_msg TEXT NULL,
		latency_ms INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY (task_id) REFERENCES ddns_tasks(id) ON DELETE CASCADE
	);`,
	`CREATE TABLE IF NOT EXISTS audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL,
		action TEXT NOT NULL,
		target_type TEXT NOT NULL,
		target_id TEXT NOT NULL,
		detail_json TEXT NULL,
		created_at DATETIME NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS system_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL CHECK (type IN ('ddns_task', 'public_ip_check', 'operation')),
		result TEXT NOT NULL,
		ddns_task_id INTEGER NULL,
		actor TEXT NULL,
		action TEXT NULL,
		target_type TEXT NULL,
		target_id TEXT NULL,
		old_ip TEXT NULL,
		new_ip TEXT NULL,
		error_msg TEXT NULL,
		latency_ms INTEGER NULL,
		detail_json TEXT NULL,
		created_at DATETIME NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS system_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS public_ip_check_settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
		interval_sec INTEGER NOT NULL DEFAULT 300 CHECK (interval_sec > 0),
		ip_sources_json TEXT NOT NULL,
		public_ip TEXT NULL,
		last_checked_at DATETIME NULL,
		updated_at DATETIME NOT NULL
	);`,
	`INSERT INTO public_ip_check_settings(id, enabled, interval_sec, ip_sources_json, updated_at)
		SELECT 1, 1, 300, value, COALESCE(updated_at, CURRENT_TIMESTAMP)
		FROM system_settings
		WHERE key = 'ip_sources'
			AND NOT EXISTS (SELECT 1 FROM public_ip_check_settings WHERE id = 1);`,
	`CREATE INDEX IF NOT EXISTS idx_domains_vendor_id ON domains(vendor_id);`,
	`CREATE INDEX IF NOT EXISTS idx_records_domain_id ON records(domain_id);`,
	`CREATE INDEX IF NOT EXISTS idx_ddns_tasks_status_next_run_at ON ddns_tasks(status, next_run_at);`,
	`CREATE INDEX IF NOT EXISTS idx_ddns_logs_task_id_created_at ON ddns_logs(task_id, created_at);`,
	`CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);`,
	`CREATE INDEX IF NOT EXISTS idx_system_logs_created_at ON system_logs(created_at);`,
	`CREATE INDEX IF NOT EXISTS idx_system_logs_type_result_created_at ON system_logs(type, result, created_at);`,
	`CREATE INDEX IF NOT EXISTS idx_system_logs_ddns_task_created_at ON system_logs(ddns_task_id, created_at);`,
}

// SchemaStatements returns a copy of all migration statements used to bootstrap SQLite.
func SchemaStatements() []string {
	out := make([]string, len(schemaStatements))
	copy(out, schemaStatements)
	return out
}

// Open creates a SQLite database handle with baseline pragmas.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("open sqlite: empty path")
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	conn.SetMaxOpenConns(1)
	conn.SetConnMaxLifetime(0)
	conn.SetConnMaxIdleTime(5 * time.Minute)

	if _, err := conn.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	return conn, nil
}

// Migrate creates and upgrades schema objects to the latest version.
func Migrate(ctx context.Context, conn *sql.DB) error {
	if conn == nil {
		return fmt.Errorf("migrate schema: nil db connection")
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}

	for _, stmt := range schemaStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration statement: %w", err)
		}
	}
	if err := ensureColumnExists(ctx, tx, "public_ip_check_settings", "public_ip", `ALTER TABLE public_ip_check_settings ADD COLUMN public_ip TEXT NULL`); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := ensureColumnExists(ctx, tx, "public_ip_check_settings", "last_checked_at", `ALTER TABLE public_ip_check_settings ADD COLUMN last_checked_at DATETIME NULL`); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration tx: %w", err)
	}

	return nil
}

func ensureColumnExists(ctx context.Context, tx *sql.Tx, table, column, addColumnStmt string) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("query table info for %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan table info for %s: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table info for %s: %w", table, err)
	}

	if _, err := tx.ExecContext(ctx, addColumnStmt); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}
