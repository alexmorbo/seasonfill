//go:build integration

package mediastore

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestS3Store_RoundTrip drives the full contract suite against a real
// S3 endpoint. The test is gated two ways: the integration build tag
// keeps it out of `make test`, and the MEDIASTORE_S3_TEST_ENDPOINT
// env var lets the operator skip it when no minio container is
// available (e.g. CI without docker).
//
// Configure with:
//
//	MEDIASTORE_S3_TEST_ENDPOINT=http://localhost:9000
//	MEDIASTORE_S3_TEST_BUCKET=seasonfill-test
//	MEDIASTORE_S3_TEST_ACCESS_KEY=minioadmin
//	MEDIASTORE_S3_TEST_SECRET_KEY=minioadmin
func TestS3Store_RoundTrip(t *testing.T) {
	endpoint := os.Getenv("MEDIASTORE_S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("MEDIASTORE_S3_TEST_ENDPOINT not set; skipping s3 integration test")
	}
	cfg := S3Config{
		Endpoint:  endpoint,
		Bucket:    envOr("MEDIASTORE_S3_TEST_BUCKET", "seasonfill-test"),
		AccessKey: envOr("MEDIASTORE_S3_TEST_ACCESS_KEY", "minioadmin"),
		SecretKey: envOr("MEDIASTORE_S3_TEST_SECRET_KEY", "minioadmin"),
		Region:    "us-east-1",
	}
	ctx := context.Background()
	s, err := newS3Store(ctx, cfg)
	require.NoError(t, err)

	// 1 MiB round-trip — matches PRD acceptance §6.5 "1MB blob".
	blob := make([]byte, 1024*1024)
	_, err = rand.Read(blob)
	require.NoError(t, err)
	key := Key("https://example.com/s3-integration-1mb", "bin")
	t.Cleanup(func() { _ = s.Delete(ctx, key) })

	require.NoError(t, s.Put(ctx, key, bytes.NewReader(blob), int64(len(blob)), "application/octet-stream"))

	rc, info, err := s.Get(ctx, key)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })
	require.Equal(t, int64(len(blob)), info.Size)

	buf := make([]byte, len(blob))
	_, err = io.ReadFull(rc, buf)
	require.NoError(t, err)
	require.Equal(t, blob, buf)
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
