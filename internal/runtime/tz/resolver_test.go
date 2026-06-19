package tz

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore is a minimal in-memory Store impl.
type fakeStore struct {
	mu   sync.Mutex
	tz   string
	read error
	save error
}

func (s *fakeStore) GetTimezone(_ context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.read != nil {
		return "", s.read
	}
	return s.tz, nil
}

func (s *fakeStore) SetTimezone(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.save != nil {
		return s.save
	}
	s.tz = name
	return nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNew_FallsBackToUTC_WhenNoStoreNoEnv(t *testing.T) {
	t.Setenv("TZ", "")
	r := New(context.Background(), nil, quietLogger())
	assert.Equal(t, time.UTC, r.Get())
	assert.Equal(t, SourceDefault, r.Source())
	assert.Equal(t, "UTC", r.Name())
}

func TestNew_UsesEnv_WhenStoreEmpty(t *testing.T) {
	t.Setenv("TZ", "Europe/Moscow")
	r := New(context.Background(), &fakeStore{tz: ""}, quietLogger())
	assert.Equal(t, SourceEnv, r.Source())
	assert.Equal(t, "Europe/Moscow", r.Name())
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	assert.Equal(t, loc, r.Get())
}

func TestNew_DBOverridesEnv(t *testing.T) {
	t.Setenv("TZ", "Europe/Moscow")
	r := New(context.Background(), &fakeStore{tz: "America/New_York"}, quietLogger())
	assert.Equal(t, SourceDB, r.Source())
	assert.Equal(t, "America/New_York", r.Name())
}

func TestNew_InvalidEnv_FallsBackToUTC(t *testing.T) {
	t.Setenv("TZ", "Not/A/Real/Zone")
	r := New(context.Background(), &fakeStore{tz: ""}, quietLogger())
	assert.Equal(t, SourceDefault, r.Source())
}

func TestNew_InvalidDBValue_FallsBackToEnv(t *testing.T) {
	t.Setenv("TZ", "Europe/Moscow")
	r := New(context.Background(), &fakeStore{tz: "Bad/Zone"}, quietLogger())
	assert.Equal(t, SourceEnv, r.Source())
	assert.Equal(t, "Europe/Moscow", r.Name())
}

func TestNew_StoreReadError_FallsBackToEnv(t *testing.T) {
	t.Setenv("TZ", "Europe/Moscow")
	store := &fakeStore{read: errors.New("db down")}
	r := New(context.Background(), store, quietLogger())
	assert.Equal(t, SourceEnv, r.Source())
}

func TestSet_ValidName_Persists(t *testing.T) {
	t.Setenv("TZ", "")
	store := &fakeStore{}
	r := New(context.Background(), store, quietLogger())

	require.NoError(t, r.Set(context.Background(), "Asia/Tokyo"))
	assert.Equal(t, SourceDB, r.Source())
	assert.Equal(t, "Asia/Tokyo", r.Name())
	assert.Equal(t, "Asia/Tokyo", store.tz)
}

func TestSet_InvalidName_RejectsAndReturnsErr(t *testing.T) {
	t.Setenv("TZ", "")
	store := &fakeStore{}
	r := New(context.Background(), store, quietLogger())

	err := r.Set(context.Background(), "Not/A/Zone")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTimezone)
	assert.Equal(t, "", store.tz, "no persistence on validation failure")
	assert.Equal(t, SourceDefault, r.Source(), "in-memory state unchanged on failure")
}

func TestSet_Empty_ClearsToEnv(t *testing.T) {
	t.Setenv("TZ", "Europe/Moscow")
	store := &fakeStore{tz: "America/New_York"}
	r := New(context.Background(), store, quietLogger())
	require.Equal(t, SourceDB, r.Source())

	require.NoError(t, r.Set(context.Background(), ""))
	assert.Equal(t, SourceEnv, r.Source())
	assert.Equal(t, "Europe/Moscow", r.Name())
	assert.Equal(t, "", store.tz, "DB cleared")
}

func TestSet_Empty_ClearsToDefault_WhenNoEnv(t *testing.T) {
	t.Setenv("TZ", "")
	store := &fakeStore{tz: "America/New_York"}
	r := New(context.Background(), store, quietLogger())

	require.NoError(t, r.Set(context.Background(), ""))
	assert.Equal(t, SourceDefault, r.Source())
	assert.Equal(t, "UTC", r.Name())
}

func TestSet_StoreWriteError_DoesNotSwap(t *testing.T) {
	t.Setenv("TZ", "")
	store := &fakeStore{save: errors.New("disk full")}
	r := New(context.Background(), store, quietLogger())
	before := r.Source()

	err := r.Set(context.Background(), "Asia/Tokyo")
	require.Error(t, err)
	assert.Equal(t, before, r.Source(), "no swap on persist failure")
}

func TestGet_NeverNil(t *testing.T) {
	t.Setenv("TZ", "")
	r := New(context.Background(), nil, quietLogger())
	// Race a bunch of Gets against a Set.
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 100 {
				assert.NotNil(t, r.Get())
			}
		})
	}
	for range 10 {
		wg.Go(func() {
			_ = r.Set(context.Background(), "UTC")
			_ = r.Set(context.Background(), "")
		})
	}
	wg.Wait()
}
