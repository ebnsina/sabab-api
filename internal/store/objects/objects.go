// Package objects stores release artifacts: source maps today, native debug
// files later.
//
// Artifacts are large, immutable and read rarely — once per symbolication, and
// then cached. That profile belongs in object storage, not in a database, which
// is why the bytes live here and only the index lives in Postgres.
package objects

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ebnsina/sabab-api/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound means no object exists under that key.
var ErrNotFound = errors.New("artifact not found")

// Store is an S3-compatible object store (MinIO when self-hosted).
type Store struct {
	client *minio.Client
	bucket string
}

// Connect opens the store and verifies the bucket exists, so a
// misconfigured deploy fails at boot rather than on the first source-map upload.
func Connect(ctx context.Context, cfg config.S3) (*Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("connect object store: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %q does not exist", cfg.Bucket)
	}
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Put stores an artifact. size may be -1 when it is not known up front.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put artifact %q: %w", key, err)
	}
	return nil
}

// Get reads an artifact. The caller closes the reader.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get artifact %q: %w", key, err)
	}
	// GetObject is lazy — it does not talk to the server until the first read,
	// so a missing key surfaces here rather than at Get. Stat forces the round
	// trip now, so callers get ErrNotFound instead of a surprise mid-stream.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return nil, fmt.Errorf("stat artifact %q: %w", key, err)
	}
	return obj, nil
}

// Ping satisfies health.Check.
func (s *Store) Ping(ctx context.Context) error {
	if _, err := s.client.BucketExists(ctx, s.bucket); err != nil {
		return fmt.Errorf("object store unreachable: %w", err)
	}
	return nil
}
