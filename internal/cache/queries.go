package cache

import (
	"database/sql"
	"time"

	"github.com/jpdarago/lazysnap/internal/tarsnap"
)

// GetArchives returns cached archives, or nil if the cache is empty.
func (db *DB) GetArchives() ([]tarsnap.Archive, error) {
	rows, err := db.conn.Query("SELECT name, created_at FROM archives ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var archives []tarsnap.Archive
	for rows.Next() {
		var a tarsnap.Archive
		var ts string
		if err := rows.Scan(&a.Name, &ts); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		archives = append(archives, a)
	}
	return archives, rows.Err()
}

// PutArchives replaces all cached archives.
func (db *DB) PutArchives(archives []tarsnap.Archive) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM archives"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO archives (name, created_at) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, a := range archives {
		if _, err := stmt.Exec(a.Name, a.CreatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetStats returns cached stats for an archive, or nil if not cached.
func (db *DB) GetStats(archiveName string) (*tarsnap.ArchiveStats, error) {
	var s tarsnap.ArchiveStats
	err := db.conn.QueryRow(
		"SELECT archive_name, total_size, compressed_size, unique_size, unique_comp_size FROM archive_stats WHERE archive_name = ?",
		archiveName,
	).Scan(&s.ArchiveName, &s.TotalSize, &s.CompressedSize, &s.UniqueSize, &s.UniqueCompSize)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// PutStats caches stats for an archive.
func (db *DB) PutStats(s *tarsnap.ArchiveStats) error {
	_, err := db.conn.Exec(
		"INSERT OR REPLACE INTO archive_stats (archive_name, total_size, compressed_size, unique_size, unique_comp_size) VALUES (?, ?, ?, ?, ?)",
		s.ArchiveName, s.TotalSize, s.CompressedSize, s.UniqueSize, s.UniqueCompSize,
	)
	return err
}

// GetFiles returns cached file entries for an archive, or nil if not cached.
func (db *DB) GetFiles(archiveName string) ([]tarsnap.FileEntry, error) {
	rows, err := db.conn.Query(
		"SELECT path, is_dir FROM archive_files WHERE archive_name = ? ORDER BY path",
		archiveName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []tarsnap.FileEntry
	for rows.Next() {
		var f tarsnap.FileEntry
		if err := rows.Scan(&f.Path, &f.IsDir); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// PutFiles caches file entries for an archive.
func (db *DB) PutFiles(archiveName string, files []tarsnap.FileEntry) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM archive_files WHERE archive_name = ?", archiveName); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO archive_files (archive_name, path, is_dir) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.Exec(archiveName, f.Path, f.IsDir); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchFiles finds archives containing files whose path matches a substring.
// Returns a map of archive name to matching file paths.
func (db *DB) SearchFiles(query string) (map[string][]string, error) {
	rows, err := db.conn.Query(
		"SELECT archive_name, path FROM archive_files WHERE path LIKE ? ORDER BY archive_name, path",
		"%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make(map[string][]string)
	for rows.Next() {
		var archive, path string
		if err := rows.Scan(&archive, &path); err != nil {
			return nil, err
		}
		results[archive] = append(results[archive], path)
	}
	return results, rows.Err()
}

// ClearStats removes all cached stats so they are re-fetched.
func (db *DB) ClearStats() error {
	_, err := db.conn.Exec("DELETE FROM archive_stats")
	return err
}

// SetRestored records when and where an archive was last restored.
func (db *DB) SetRestored(archiveName, targetDir string) error {
	_, err := db.conn.Exec(
		"UPDATE archives SET last_restored_at = ?, last_restored_to = ? WHERE name = ?",
		time.Now().Format(time.RFC3339), targetDir, archiveName,
	)
	return err
}

// GetRestoreInfo returns the last restore time and directory for an archive, or zero values if never restored.
func (db *DB) GetRestoreInfo(archiveName string) (time.Time, string) {
	var ts, dir sql.NullString
	db.conn.QueryRow(
		"SELECT last_restored_at, last_restored_to FROM archives WHERE name = ?",
		archiveName,
	).Scan(&ts, &dir)
	if !ts.Valid {
		return time.Time{}, ""
	}
	t, _ := time.Parse(time.RFC3339, ts.String)
	return t, dir.String
}

// GetConfig returns a config value, or empty string if not set.
func (db *DB) GetConfig(key string) string {
	var val string
	err := db.conn.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&val)
	if err != nil {
		return ""
	}
	return val
}

// SetConfig stores a config value.
func (db *DB) SetConfig(key, value string) error {
	_, err := db.conn.Exec("INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)", key, value)
	return err
}

// DeleteArchive removes an archive and its associated data from the cache.
func (db *DB) DeleteArchive(name string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, q := range []string{
		"DELETE FROM archive_files WHERE archive_name = ?",
		"DELETE FROM archive_stats WHERE archive_name = ?",
		"DELETE FROM archives WHERE name = ?",
	} {
		if _, err := tx.Exec(q, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}
