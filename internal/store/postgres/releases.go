package postgres

import (
	"context"
	"fmt"
)

// Release is a deployed version of a project.
type Release struct {
	ID        uint64
	ProjectID uint64
	Version   string // "web@2.4.1"
	Ref       string // git sha
}

// ReleaseFile indexes one uploaded artifact. The bytes are in S3; this is the
// pointer to them.
type ReleaseFile struct {
	ID          uint64
	ReleaseID   uint64
	URLPattern  string // "~/static/js/main.a3f9.js"
	ArtifactKey string // S3 object key
	SizeBytes   int64
	Checksum    string
}

// UpsertRelease creates a release, or returns the existing one.
//
// Idempotent because a CI pipeline may upload artifacts in several steps, or be
// re-run after a failure. A second upload must not be an error.
func (db *DB) UpsertRelease(ctx context.Context, projectID uint64, version, ref string) (Release, error) {
	const query = `
		INSERT INTO releases (project_id, version, ref)
		VALUES ($1, $2, NULLIF($3, ''))
		ON CONFLICT (project_id, version) DO UPDATE SET
			ref = COALESCE(NULLIF(EXCLUDED.ref, ''), releases.ref)
		RETURNING id, project_id, version, COALESCE(ref, '')`

	var r Release
	err := db.QueryRow(ctx, query, projectID, version, ref).
		Scan(&r.ID, &r.ProjectID, &r.Version, &r.Ref)
	if err != nil {
		return Release{}, fmt.Errorf("upsert release: %w", err)
	}
	return r, nil
}

// UpsertReleaseFile indexes an artifact. Re-uploading the same url_pattern
// replaces it, so a corrected source map can be pushed without deleting first.
func (db *DB) UpsertReleaseFile(ctx context.Context, f ReleaseFile) error {
	const query = `
		INSERT INTO release_files (release_id, url_pattern, artifact_key, size_bytes, checksum)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (release_id, url_pattern) DO UPDATE SET
			artifact_key = EXCLUDED.artifact_key,
			size_bytes   = EXCLUDED.size_bytes,
			checksum     = EXCLUDED.checksum`

	_, err := db.Exec(ctx, query, f.ReleaseID, f.URLPattern, f.ArtifactKey, f.SizeBytes, f.Checksum)
	if err != nil {
		return fmt.Errorf("upsert release file: %w", err)
	}
	return nil
}

// ReleaseFilesFor lists every artifact uploaded for a release.
//
// The whole list is returned rather than querying per frame: a stack has many
// frames, they almost all come from the same handful of bundles, and one query
// per frame would make symbolication N round trips per event.
func (db *DB) ReleaseFilesFor(ctx context.Context, projectID uint64, version string) ([]ReleaseFile, error) {
	const query = `
		SELECT f.id, f.release_id, f.url_pattern, f.artifact_key, f.size_bytes, f.checksum
		FROM release_files f
		JOIN releases r ON r.id = f.release_id
		WHERE r.project_id = $1 AND r.version = $2`

	rows, err := db.Query(ctx, query, projectID, version)
	if err != nil {
		return nil, fmt.Errorf("list release files: %w", err)
	}
	defer rows.Close()

	var files []ReleaseFile
	for rows.Next() {
		var f ReleaseFile
		if err := rows.Scan(&f.ID, &f.ReleaseID, &f.URLPattern, &f.ArtifactKey, &f.SizeBytes, &f.Checksum); err != nil {
			return nil, fmt.Errorf("scan release file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
