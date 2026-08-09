package gc

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
)

// TestMediaSweep_FSStore_EndToEnd certifies the weekly media sweep
// against a real filesystem-backed store (mediaStore.mode=fs / PVC
// deploys), not a mock. Two blobs are written to disk; one hash is in
// the live set, the other is orphaned. After the real sweep runs, the
// orphaned file MUST be gone from disk and the referenced one MUST
// remain — proving GC is store-agnostic and covers the fs backend.
func TestMediaSweep_FSStore_EndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store, err := mediastore.New(ctx, mediastore.Config{
		Mode:   mediastore.ModeFS,
		FSPath: t.TempDir(),
	})
	require.NoError(t, err)

	const (
		keepHash  = "keephash"
		dropHash  = "drophash"
		keepURL   = "https://image.tmdb.org/t/p/w342/keep.jpg"
		dropURL   = "https://image.tmdb.org/t/p/w342/drop.jpg"
		mediaType = "image/jpeg"
	)
	keepKey := mediastore.Key(keepURL, extFromContentType(mediaType))
	dropKey := mediastore.Key(dropURL, extFromContentType(mediaType))

	body := []byte("some jpeg bytes")
	require.NoError(t, store.Put(ctx, keepKey, bytes.NewReader(body), int64(len(body)), mediaType))
	require.NoError(t, store.Put(ctx, dropKey, bytes.NewReader(body), int64(len(body)), mediaType))

	cold := &fakeColdAssets{rows: []coldRow{
		{hash: keepHash, sourceURL: keepURL, contentType: mediaType},
		{hash: dropHash, sourceURL: dropURL, contentType: mediaType},
	}}

	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{hashes: map[string]struct{}{keepHash: {}}},
		Assets:  cold,
		Store:   store,
	}.Build()

	res, err := build(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, res.Candidates)
	assert.Equal(t, 1, res.Deleted)
	assert.Equal(t, 0, res.StoreFailures)
	assert.Equal(t, []string{dropHash}, cold.deletedHash)

	// Orphaned blob is gone from disk.
	_, statErr := store.Stat(ctx, dropKey)
	assert.ErrorIs(t, statErr, mediastore.ErrNotFound)

	// Referenced blob remains on disk untouched.
	info, keepErr := store.Stat(ctx, keepKey)
	require.NoError(t, keepErr)
	assert.Equal(t, int64(len(body)), info.Size)
}
