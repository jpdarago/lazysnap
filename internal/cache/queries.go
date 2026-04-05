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

// ClearStats removes all cached stats so they are re-fetched.
func (db *DB) ClearStats() error {
	_, err := db.conn.Exec("DELETE FROM archive_stats")
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
