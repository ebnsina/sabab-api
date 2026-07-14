// Command sabab is the CLI. Today it uploads source maps for a release.
//
//	sabab sourcemaps upload \
//	  --project 1 --release web@2.4.1 \
//	  --url-prefix '~/static/js' ./dist/static/js
//
// This is the step a build pipeline runs after bundling. Without it, stacks stay
// minified — so it has to be one command with no ceremony, or people will skip
// it and then wonder why the product is useless.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/ebnsina/sabab-api/internal/logging"
	"github.com/ebnsina/sabab-api/internal/store/objects"
	"github.com/ebnsina/sabab-api/internal/store/postgres"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "sourcemaps" || os.Args[2] != "upload" {
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet("sourcemaps upload", flag.ExitOnError)
	var (
		projectID = fs.Uint64("project", 0, "project id (required)")
		release   = fs.String("release", "", "release version, e.g. web@2.4.1 (required)")
		ref       = fs.String("ref", "", "git sha or tag")
		urlPrefix = fs.String("url-prefix", "~/", "how the browser will see these files, e.g. ~/static/js")
	)
	if err := fs.Parse(os.Args[3:]); err != nil {
		os.Exit(2)
	}

	dir := fs.Arg(0)
	if *projectID == 0 || *release == "" || dir == "" {
		usage()
		os.Exit(2)
	}

	if err := run(context.Background(), *projectID, *release, *ref, *urlPrefix, dir); err != nil {
		slog.Default().Error("upload failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `sabab sourcemaps upload — upload source maps for a release

  sabab sourcemaps upload --project <id> --release <version> [--ref <sha>] \
      [--url-prefix '~/static/js'] <directory>

The url-prefix is how the BROWSER will name these files. "~" means "any host",
so a map for https://app.example.com/static/js/main.a3f9.js is uploaded with
--url-prefix '~/static/js'. Get this wrong and the maps are stored but never
matched, and stacks stay minified with no error to explain why.
`)
}

func run(ctx context.Context, projectID uint64, release, ref, urlPrefix, dir string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logging.Service(logging.New(cfg.LogLevel, cfg.Env), "sabab")

	db, err := postgres.Connect(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()

	store, err := objects.Connect(ctx, cfg.S3)
	if err != nil {
		return err
	}

	rel, err := db.UpsertRelease(ctx, projectID, release, ref)
	if err != nil {
		return err
	}

	maps, err := findSourceMaps(dir)
	if err != nil {
		return err
	}
	if len(maps) == 0 {
		return fmt.Errorf("no .map files found under %s — did the build emit source maps?", dir)
	}

	for _, path := range maps {
		if err := upload(ctx, db, store, rel, projectID, release, urlPrefix, dir, path, log); err != nil {
			return err
		}
	}

	log.Info("upload complete",
		slog.String("release", release),
		slog.Int("files", len(maps)))
	return nil
}

// upload stores one map and indexes it.
func upload(
	ctx context.Context,
	db *postgres.DB,
	store *objects.Store,
	rel postgres.Release,
	projectID uint64,
	release, urlPrefix, root, path string,
	log *slog.Logger,
) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	relative, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("relativize %s: %w", path, err)
	}

	// The pattern is how the BROWSER will name the *minified file* — so the
	// ".map" suffix comes off. A frame reports main.a3f9.js, never
	// main.a3f9.js.map, and matching the wrong one means the map never applies.
	pattern := joinURL(urlPrefix, strings.TrimSuffix(filepath.ToSlash(relative), ".map"))

	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])

	// Keyed by content hash: re-uploading an unchanged map is free, and two
	// releases sharing an identical chunk store it once.
	key := fmt.Sprintf("sourcemaps/%d/%s/%s.map", projectID, release, checksum)

	if err := store.Put(ctx, key, strings.NewReader(string(body)), int64(len(body)), "application/json"); err != nil {
		return err
	}
	if err := db.UpsertReleaseFile(ctx, postgres.ReleaseFile{
		ReleaseID:   rel.ID,
		URLPattern:  pattern,
		ArtifactKey: key,
		SizeBytes:   int64(len(body)),
		Checksum:    checksum,
	}); err != nil {
		return err
	}

	log.Info("uploaded", slog.String("pattern", pattern), slog.Int("bytes", len(body)))
	return nil
}

func findSourceMaps(dir string) ([]string, error) {
	var maps []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".map") {
			maps = append(maps, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("directory %s does not exist", dir)
		}
		return nil, fmt.Errorf("scan %s: %w", dir, err)
	}
	return maps, nil
}

func joinURL(prefix, rel string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	rel = strings.TrimPrefix(rel, "/")
	return prefix + "/" + rel
}
