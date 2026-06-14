package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appmedia "github.com/alexmorbo/seasonfill/application/media"
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
	r := NewMediaResolver(&fakeMediaLookup{}, nil, nil, silentResolverLogger())
	require.Nil(t, r.Resolve(context.Background(), nil, "w342", "poster_w342"))
}

func TestMediaResolver_Empty_PathReturnsNil(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{}, nil, nil, silentResolverLogger())
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
	}}, nil, nil, silentResolverLogger())
	p := "/abc.jpg"
	got := r.Resolve(context.Background(), &p, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, hash, *got)
}

func TestMediaResolver_UnknownPath_ReturnsNil(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{byURL: map[string]string{}}, nil, nil, silentResolverLogger())
	p := "/nope.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestMediaResolver_LookupError_ReturnsNil_DoesNotPanic(t *testing.T) {
	r := NewMediaResolver(&fakeMediaLookup{err: errors.New("db down")}, nil, nil, silentResolverLogger())
	p := "/abc.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestMediaResolver_DifferentSize_DifferentURL(t *testing.T) {
	const hashGrid = "0000000000000000000000000000000000000000000000000000000000000001"
	r := NewMediaResolver(&fakeMediaLookup{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/abc.jpg": hashGrid,
		// w780 deliberately absent — request at w780 must miss.
	}}, nil, nil, silentResolverLogger())
	p := "/abc.jpg"
	got := r.Resolve(context.Background(), &p, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, hashGrid, *got)

	missing := r.Resolve(context.Background(), &p, "w780", "poster_w780")
	require.Nil(t, missing)
}

// --- Story 316 tests ---

type stubEnqueuer struct {
	calls [][]appmedia.EnqueueRequest
}

func (s *stubEnqueuer) Enqueue(_ context.Context, reqs []appmedia.EnqueueRequest) {
	s.calls = append(s.calls, reqs)
}

type stubFetcher struct {
	hash string
	ok   bool
	last struct {
		url, kind, ext string
	}
}

func (s *stubFetcher) FetchSync(_ context.Context, url, kind, ext string) (string, bool) {
	s.last.url, s.last.kind, s.last.ext = url, kind, ext
	return s.hash, s.ok
}

func TestMediaResolver_Resolve_MissEnqueuesAsync(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{} // returns ports.ErrNotFound by default
	enq := &stubEnqueuer{}
	r := NewMediaResolver(lookup, enq, nil, silentResolverLogger())
	path := "/abc.jpg"
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	assert.Nil(t, got)
	require.Len(t, enq.calls, 1)
	require.Len(t, enq.calls[0], 1)
	assert.Contains(t, enq.calls[0][0].UpstreamURL, "/w342/abc.jpg")
	assert.Equal(t, "poster_w342", enq.calls[0][0].Kind)
	assert.Equal(t, "jpg", enq.calls[0][0].Extension)
}

func TestMediaResolver_ResolveSync_MissCallsFetchSync(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	enq := &stubEnqueuer{}
	fetcher := &stubFetcher{hash: "deadbeef", ok: true}
	r := NewMediaResolver(lookup, enq, fetcher, silentResolverLogger())
	path := "/abc.jpg"
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, "deadbeef", *got)
	assert.Equal(t, "poster_w342", fetcher.last.kind)
	assert.Equal(t, "jpg", fetcher.last.ext)
	// On sync success, no async enqueue.
	assert.Empty(t, enq.calls)
}

func TestMediaResolver_ResolveSync_FetchFailFallsBackToEnqueue(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	enq := &stubEnqueuer{}
	fetcher := &stubFetcher{ok: false}
	r := NewMediaResolver(lookup, enq, fetcher, silentResolverLogger())
	path := "/abc.jpg"
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	assert.Nil(t, got)
	require.Len(t, enq.calls, 1, "fallback async enqueue expected")
}

func TestMediaResolver_SetSideEffects_LateBind(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	r := NewMediaResolver(lookup, nil, nil, silentResolverLogger())
	// Pre-binding: Resolve on miss must be safe (no enqueue, no panic).
	path := "/abc.jpg"
	require.Nil(t, r.Resolve(t.Context(), &path, "w342", "poster_w342"))
	// Late-bind enqueuer + fetcher.
	enq := &stubEnqueuer{}
	fetcher := &stubFetcher{hash: "cafebabe", ok: true}
	r.SetSideEffects(enq, fetcher)
	// Resolve miss now enqueues.
	require.Nil(t, r.Resolve(t.Context(), &path, "w342", "poster_w342"))
	require.Len(t, enq.calls, 1)
	// ResolveSync miss now sync-fetches.
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, "cafebabe", *got)
}
