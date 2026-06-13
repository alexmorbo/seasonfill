package gc

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
)

type fakeLiveHashes struct {
	hashes map[string]struct{}
	err    error
}

func (f *fakeLiveHashes) CollectLiveAssetHashes(_ context.Context) (map[string]struct{}, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hashes, nil
}

type coldRow struct {
	hash, sourceURL, contentType string
}

type fakeColdAssets struct {
	rows        []coldRow
	iterateErr  error
	deletedHash []string
	deleteErr   map[string]error
}

func (f *fakeColdAssets) IterateColdAssets(_ context.Context, _ time.Time, _ int, fn func(hash, sourceURL, contentType string) error) error {
	if f.iterateErr != nil {
		return f.iterateErr
	}
	for _, r := range f.rows {
		if err := fn(r.hash, r.sourceURL, r.contentType); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeColdAssets) DeleteByHash(_ context.Context, hash string) error {
	if err, ok := f.deleteErr[hash]; ok {
		return err
	}
	f.deletedHash = append(f.deletedHash, hash)
	return nil
}

type fakeStore struct {
	deletedKeys []string
	deleteErr   map[string]error
}

func (s *fakeStore) Get(_ context.Context, _ string) (io.ReadCloser, mediastore.ObjectInfo, error) {
	return nil, mediastore.ObjectInfo{}, mediastore.ErrNotFound
}
func (s *fakeStore) Put(_ context.Context, _ string, _ io.Reader, _ int64, _ string) error {
	return nil
}
func (s *fakeStore) Stat(_ context.Context, _ string) (mediastore.ObjectInfo, error) {
	return mediastore.ObjectInfo{}, mediastore.ErrNotFound
}
func (s *fakeStore) Delete(_ context.Context, key string) error {
	if err, ok := s.deleteErr[key]; ok {
		return err
	}
	s.deletedKeys = append(s.deletedKeys, key)
	return nil
}
func (s *fakeStore) List(_ context.Context, _ string, _ func(mediastore.ObjectInfo) error) error {
	return mediastore.ErrNotSupported
}

func TestMediaSweep_LiveHash_Skipped(t *testing.T) {
	t.Parallel()
	live := map[string]struct{}{"keep": {}}
	cold := &fakeColdAssets{rows: []coldRow{{hash: "keep", sourceURL: "u1", contentType: "image/jpeg"}}}
	store := &fakeStore{}
	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{hashes: live},
		Assets:  cold,
		Store:   store,
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Candidates)
	assert.Equal(t, 0, res.Deleted)
	assert.Empty(t, cold.deletedHash)
	assert.Empty(t, store.deletedKeys)
}

func TestMediaSweep_ColdNonLive_Deleted(t *testing.T) {
	t.Parallel()
	cold := &fakeColdAssets{rows: []coldRow{{hash: "drop", sourceURL: "https://x/img.jpg", contentType: "image/jpeg"}}}
	store := &fakeStore{}
	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{hashes: map[string]struct{}{}},
		Assets:  cold,
		Store:   store,
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Candidates)
	assert.Equal(t, 1, res.Deleted)
	assert.Equal(t, []string{"drop"}, cold.deletedHash)
	assert.Len(t, store.deletedKeys, 1)
}

func TestMediaSweep_StoreNotFound_RowStillDrops(t *testing.T) {
	t.Parallel()
	cold := &fakeColdAssets{rows: []coldRow{{hash: "drop", sourceURL: "https://x/img.jpg", contentType: "image/jpeg"}}}
	store := &fakeStore{deleteErr: map[string]error{}}
	// Build the expected key first to make deleteErr keyed.
	key := mediastore.Key("https://x/img.jpg", "jpg")
	store.deleteErr[key] = mediastore.ErrNotFound

	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{hashes: map[string]struct{}{}},
		Assets:  cold,
		Store:   store,
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted)
	assert.Equal(t, 0, res.StoreFailures)
	assert.Equal(t, []string{"drop"}, cold.deletedHash)
}

func TestMediaSweep_StoreHardError_RowKept(t *testing.T) {
	t.Parallel()
	cold := &fakeColdAssets{rows: []coldRow{{hash: "drop", sourceURL: "https://x/img.jpg", contentType: "image/jpeg"}}}
	key := mediastore.Key("https://x/img.jpg", "jpg")
	store := &fakeStore{deleteErr: map[string]error{key: errors.New("permission denied")}}

	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{hashes: map[string]struct{}{}},
		Assets:  cold,
		Store:   store,
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Deleted)
	assert.Equal(t, 1, res.StoreFailures)
	assert.Empty(t, cold.deletedHash)
}

func TestMediaSweep_NilStore_RowDeleteOnly(t *testing.T) {
	t.Parallel()
	cold := &fakeColdAssets{rows: []coldRow{{hash: "drop"}}}
	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{hashes: map[string]struct{}{}},
		Assets:  cold,
		Store:   nil,
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted)
}

func TestMediaSweep_CollectLiveError(t *testing.T) {
	t.Parallel()
	build := MediaSweepDeps{
		LiveSet: &fakeLiveHashes{err: errors.New("db down")},
		Assets:  &fakeColdAssets{},
	}.Build()
	_, err := build(context.Background())
	require.Error(t, err)
}
