package mediastore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeFactory builds a fresh Store rooted at a clean temp location.
// Used by the shared contract suite so each backend re-uses the same
// expectations.
type storeFactory func(t *testing.T) Store

func TestNullStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newNullStore()

	_, _, err := s.Get(ctx, "any")
	assert.ErrorIs(t, err, ErrNotSupported)

	assert.ErrorIs(t, s.Put(ctx, "any", bytes.NewReader(nil), 0, ""), ErrNotSupported)

	_, err = s.Stat(ctx, "any")
	assert.ErrorIs(t, err, ErrNotSupported)

	assert.ErrorIs(t, s.Delete(ctx, "any"), ErrNotSupported)
	assert.ErrorIs(t, s.List(ctx, "", func(ObjectInfo) error { return nil }), ErrNotSupported)
}

func TestFSStore_Contract(t *testing.T) {
	t.Parallel()
	runContract(t, func(t *testing.T) Store {
		t.Helper()
		s, err := newFSStore(t.TempDir())
		require.NoError(t, err)
		return s
	})
}

// runContract is the backend-agnostic acceptance suite. Reused by the
// integration test below for the s3 backend.
func runContract(t *testing.T, factory storeFactory) {
	t.Helper()
	t.Run("put then get round trips", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		key := Key("https://example.com/round-trip.jpg", "jpg")
		body := []byte("hello mediastore")
		require.NoError(t, s.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "image/jpeg"))

		rc, info, err := s.Get(ctx, key)
		require.NoError(t, err)
		t.Cleanup(func() { _ = rc.Close() })
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, body, got)
		assert.Equal(t, int64(len(body)), info.Size)
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		s := factory(t)
		_, _, err := s.Get(context.Background(), Key("https://example.com/missing", "jpg"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("stat missing returns ErrNotFound", func(t *testing.T) {
		s := factory(t)
		_, err := s.Stat(context.Background(), Key("https://example.com/missing", "jpg"))
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("delete missing is idempotent", func(t *testing.T) {
		s := factory(t)
		assert.NoError(t, s.Delete(context.Background(), Key("https://example.com/missing", "jpg")))
	})

	t.Run("list yields stored keys", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		k1 := Key("https://example.com/one", "jpg")
		k2 := Key("https://example.com/two", "jpg")
		require.NoError(t, s.Put(ctx, k1, strings.NewReader("a"), 1, "image/jpeg"))
		require.NoError(t, s.Put(ctx, k2, strings.NewReader("b"), 1, "image/jpeg"))

		seen := map[string]bool{}
		require.NoError(t, s.List(ctx, "media/v1/", func(info ObjectInfo) error {
			seen[info.Key] = true
			return nil
		}))
		assert.True(t, seen[k1])
		assert.True(t, seen[k2])
	})

	t.Run("list propagates fn error", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()
		require.NoError(t, s.Put(ctx, Key("https://example.com/abort", "jpg"), strings.NewReader("x"), 1, "image/jpeg"))
		sentinel := errors.New("abort")
		err := s.List(ctx, "media/v1/", func(ObjectInfo) error { return sentinel })
		assert.ErrorIs(t, err, sentinel)
	})
}

func TestFSStore_AtomicPut_NoPartialOnFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s, err := newFSStore(root)
	require.NoError(t, err)

	key := Key("https://example.com/atomic", "jpg")
	bad := &failingReader{data: []byte("partial"), failAfter: 3}
	err = s.Put(context.Background(), key, bad, int64(len(bad.data)), "image/jpeg")
	require.Error(t, err)

	_, statErr := s.Stat(context.Background(), key)
	require.ErrorIs(t, statErr, ErrNotFound)

	var leaked []string
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".tmp") {
			leaked = append(leaked, path)
		}
		return nil
	}))
	assert.Empty(t, leaked, "no .tmp residues expected after failed Put")
}

func TestNew_ModeDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("default mode is off", func(t *testing.T) {
		t.Parallel()
		s, err := New(ctx, Config{})
		require.NoError(t, err)
		_, putErr := s.Stat(ctx, "any")
		assert.ErrorIs(t, putErr, ErrNotSupported)
	})

	t.Run("fs mode requires path", func(t *testing.T) {
		t.Parallel()
		_, err := New(ctx, Config{Mode: ModeFS})
		require.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("fs mode constructs store", func(t *testing.T) {
		t.Parallel()
		s, err := New(ctx, Config{Mode: ModeFS, FSPath: t.TempDir()})
		require.NoError(t, err)
		require.NotNil(t, s)
	})

	t.Run("s3 mode validates required fields", func(t *testing.T) {
		t.Parallel()
		_, err := New(ctx, Config{Mode: ModeS3})
		require.ErrorIs(t, err, ErrInvalidConfig)
	})

	t.Run("unknown mode rejected", func(t *testing.T) {
		t.Parallel()
		_, err := New(ctx, Config{Mode: Mode("weird")})
		require.ErrorIs(t, err, ErrInvalidConfig)
	})
}

// failingReader emits at most failAfter bytes from data then returns
// io.ErrUnexpectedEOF, simulating a network drop or cancelled
// download mid-Put.
type failingReader struct {
	data      []byte
	off       int
	failAfter int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.off >= r.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	end := r.failAfter
	if end > len(r.data) {
		end = len(r.data)
	}
	n := copy(p, r.data[r.off:end])
	r.off += n
	if r.off >= r.failAfter {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}
