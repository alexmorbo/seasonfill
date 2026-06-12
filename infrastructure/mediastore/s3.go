package mediastore

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3Store wraps minio-go/v7 against an S3-compatible endpoint. It is
// safe for concurrent use — the embedded *minio.Client handles its
// own connection pooling.
type s3Store struct {
	client *minio.Client
	bucket string
}

func newS3Store(ctx context.Context, cfg S3Config) (Store, error) {
	endpoint, secure, err := splitEndpoint(cfg.Endpoint, cfg.UseSSL)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("mediastore s3 client: %w", err)
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("mediastore s3 bucket probe %q: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("mediastore s3: bucket %q does not exist", cfg.Bucket)
	}
	return &s3Store{client: client, bucket: cfg.Bucket}, nil
}

// splitEndpoint normalises cfg.Endpoint (which may carry a scheme) into
// the host:port form minio.New wants, returning the derived Secure
// boolean. An explicit scheme in cfg.Endpoint wins over cfg.UseSSL.
func splitEndpoint(endpoint string, useSSL bool) (string, bool, error) {
	switch {
	case endpoint == "":
		return "", false, fmt.Errorf("%w: s3 endpoint is empty", ErrInvalidConfig)
	case len(endpoint) > 8 && endpoint[:8] == "https://":
		return endpoint[8:], true, nil
	case len(endpoint) > 7 && endpoint[:7] == "http://":
		return endpoint[7:], false, nil
	default:
		return endpoint, useSSL, nil
	}
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("mediastore s3 get %q: %w", key, err)
	}
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if isNotFound(err) {
			return nil, ObjectInfo{}, fmt.Errorf("mediastore s3 get %q: %w", key, ErrNotFound)
		}
		return nil, ObjectInfo{}, fmt.Errorf("mediastore s3 stat %q: %w", key, err)
	}
	return obj, infoFrom(st), nil
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	opts := minio.PutObjectOptions{ContentType: contentType}
	if _, err := s.client.PutObject(ctx, s.bucket, key, r, size, opts); err != nil {
		return fmt.Errorf("mediastore s3 put %q: %w", key, err)
	}
	return nil
}

func (s *s3Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	st, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, fmt.Errorf("mediastore s3 stat %q: %w", key, ErrNotFound)
		}
		return ObjectInfo{}, fmt.Errorf("mediastore s3 stat %q: %w", key, err)
	}
	return infoFrom(st), nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("mediastore s3 delete %q: %w", key, err)
	}
	return nil
}

func (s *s3Store) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: true}
	for obj := range s.client.ListObjects(ctx, s.bucket, opts) {
		if obj.Err != nil {
			return fmt.Errorf("mediastore s3 list %q: %w", prefix, obj.Err)
		}
		if err := fn(infoFrom(obj)); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func infoFrom(o minio.ObjectInfo) ObjectInfo {
	return ObjectInfo{
		Key:          o.Key,
		Size:         o.Size,
		ContentType:  o.ContentType,
		ETag:         o.ETag,
		LastModified: o.LastModified.UTC(),
	}
}

// isNotFound recognises minio-go's "object missing" responses. The
// library returns a typed minio.ErrorResponse for application-level
// errors; NoSuchKey / NoSuchBucket / 404 all map to our ErrNotFound.
func isNotFound(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" || resp.StatusCode == 404
	}
	return false
}
