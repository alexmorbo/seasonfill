package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
)

type fakeMediaLookup struct {
	byURL map[string]string
	err   error
}

func (f *fakeMediaLookup) HashForSourceURL(_ context.Context, url string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	h, ok := f.byURL[url]
	if !ok {
		return "", ports.ErrNotFound
	}
	return h, nil
}

func silentResolverLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestMediaResolver_Nil_PathReturnsNil(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{}, silentResolverLogger())
	require.Nil(t, r.Resolve(context.Background(), nil, "w342", "poster_w342"))
}

func TestMediaResolver_Empty_PathReturnsNil(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{}, silentResolverLogger())
	empty := ""
	require.Nil(t, r.Resolve(context.Background(), &empty, "w342", "poster_w342"))
}

func TestMediaResolver_NoLookup_ReturnsNil(t *testing.T) {
	r := NewNopMediaResolver()
	p := "/abc.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestMediaResolver_Stored_ReturnsHash(t *testing.T) {
	const hash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	r := NewMediaResolver(&fakeMediaLookup{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/abc.jpg": hash,
	}}, silentResolverLogger())
	p := "/abc.jpg"
	got := r.Resolve(context.Background(), &p, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, hash, *got)
}

func TestMediaResolver_UnknownPath_ReturnsNil(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{byURL: map[string]string{}}, silentResolverLogger())
	p := "/nope.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestMediaResolver_LookupError_ReturnsNil_DoesNotPanic(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{err: errors.New("db down")}, silentResolverLogger())
	p := "/abc.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestMediaResolver_DifferentSize_DifferentURL(t *testing.T) {
	const hashGrid = "0000000000000000000000000000000000000000000000000000000000000001"
	r := NewMediaResolver(&fakeMediaLookup{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/abc.jpg": hashGrid,
		// w780 deliberately absent — request at w780 must miss.
	}}, silentResolverLogger())
	p := "/abc.jpg"
	got := r.Resolve(context.Background(), &p, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, hashGrid, *got)

	missing := r.Resolve(context.Background(), &p, "w780", "poster_w780")
	require.Nil(t, missing)
}
