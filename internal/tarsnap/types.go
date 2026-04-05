package tarsnap

import "time"

// Archive represents a tarsnap archive.
type Archive struct {
	Name      string
	CreatedAt time.Time
}

// ArchiveStats holds size statistics for an archive.
type ArchiveStats struct {
	ArchiveName    string
	TotalSize      int64
	CompressedSize int64
	UniqueSize     int64
	UniqueCompSize int64
}

// FileEntry represents a single file within an archive.
type FileEntry struct {
	Path        string
	Size        int64
	IsDir       bool
	Permissions string
}

// AccountBalance holds tarsnap account balance info.
type AccountBalance struct {
	Picocenters float64
	FetchedAt   time.Time
}
