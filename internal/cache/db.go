package cache

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite cache database.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the cache database.
func Open() (*DB, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open cache db: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS archives (
			name       TEXT PRIMARY KEY,
			created_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS archive_stats (
			archive_name    TEXT PRIMARY KEY REFERENCES archives(name) ON DELETE CASCADE,
			total_size      INTEGER NOT NULL,
			compressed_size INTEGER NOT NULL,
			unique_size     INTEGER NOT NULL,
			unique_comp_size INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS archive_files (
			archive_name TEXT NOT NULL REFERENCES archives(name) ON DELETE CASCADE,
			path         TEXT NOT NULL,
			is_dir       INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (archive_name, path)
		);

		CREATE TABLE IF NOT EXISTS account_balance (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			picocenters REAL NOT NULL,
			fetched_at TEXT NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func dbPath() (string, error) {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "lazysnap", "cache.db"), nil
}
