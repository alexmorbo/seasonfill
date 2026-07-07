package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

type ensurePendingCall struct {
	hash, sourceURL, kind string
}

type fakeMediaLookup struct {
	byURL         map[string]string
	err           error
	ensureCalls   []ensurePendingCall
	ensureErr     error
	lastEnsureErr error // ctx.Err() observed at the moment EnsurePending was called
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

func (f *fakeMediaLookup) EnsurePending(ctx context.Context, hash, sourceURL, kind string) error {
	f.lastEnsureErr = ctx.Err()
	f.ensureCalls = append(f.ensureCalls, ensurePendingCall{hash, sourceURL, kind})
	return f.ensureErr
}

func silentResolverLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestResolver_Nil_PathReturnsNil(t *testing.T) {
	r := NewResolver(&fakeMediaLookup{}, nil, nil, silentResolverLogger())
	require.Nil(t, r.Resolve(context.Background(), nil, "w342", "poster_w342"))
}

func TestResolver_Empty_PathReturnsNil(t *testing.T) {
	r := NewResolver(&fakeMediaLookup{}, nil, nil, silentResolverLogger())
	empty := ""
	require.Nil(t, r.Resolve(context.Background(), &empty, "w342", "poster_w342"))
}

func TestResolver_NoLookup_ReturnsNil(t *testing.T) {
	r := NewNopResolver()
	p := "/abc.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestResolver_Stored_ReturnsHash(t *testing.T) {
	const hash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	r := NewResolver(&fakeMediaLookup{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/abc.jpg": hash,
	}}, nil, nil, silentResolverLogger())
	p := "/abc.jpg"
	got := r.Resolve(context.Background(), &p, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, hash, *got)
}

func TestResolver_UnknownPath_ReturnsNil(t *testing.T) {
	r := NewResolver(&fakeMediaLookup{byURL: map[string]string{}}, nil, nil, silentResolverLogger())
	p := "/nope.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestResolver_LookupError_ReturnsNil_DoesNotPanic(t *testing.T) {
	r := NewResolver(&fakeMediaLookup{err: errors.New("db down")}, nil, nil, silentResolverLogger())
	p := "/abc.jpg"
	require.Nil(t, r.Resolve(context.Background(), &p, "w342", "poster_w342"))
}

func TestResolver_DifferentSize_DifferentURL(t *testing.T) {
	const hashGrid = "0000000000000000000000000000000000000000000000000000000000000001"
	r := NewResolver(&fakeMediaLookup{byURL: map[string]string{
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

func TestResolver_Resolve_MissEnqueuesAsync(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{} // returns ports.ErrNotFound by default
	enq := &stubEnqueuer{}
	r := NewResolver(lookup, enq, nil, silentResolverLogger())
	path := "/abc.jpg"
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	assert.Nil(t, got)
	require.Len(t, enq.calls, 1)
	require.Len(t, enq.calls[0], 1)
	assert.Contains(t, enq.calls[0][0].UpstreamURL, "/w342/abc.jpg")
	assert.Equal(t, "poster_w342", enq.calls[0][0].Kind)
	assert.Equal(t, "jpg", enq.calls[0][0].Extension)
}

func TestResolver_ResolveSync_MissCallsFetchSync(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	enq := &stubEnqueuer{}
	fetcher := &stubFetcher{hash: "deadbeef", ok: true}
	r := NewResolver(lookup, enq, fetcher, silentResolverLogger())
	path := "/abc.jpg"
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, "deadbeef", *got)
	assert.Equal(t, "poster_w342", fetcher.last.kind)
	assert.Equal(t, "jpg", fetcher.last.ext)
	// On sync success, no async enqueue.
	assert.Empty(t, enq.calls)
}

func TestResolver_ResolveSync_FetchFailReturnsEagerHash(t *testing.T) {
	t.Parallel()
	// Story 320: fetcher-miss no longer short-circuits to nil — the
	// resolver mints the deterministic sha256-hex of the URL, writes a
	// pending media_assets row, and returns the eager hash so the
	// handler can recover on the user's GET /api/v1/media/:hash.
	lookup := &fakeMediaLookup{}
	enq := &stubEnqueuer{}
	fetcher := &stubFetcher{ok: false}
	r := NewResolver(lookup, enq, fetcher, silentResolverLogger())
	path := "/abc.jpg"
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got, "eager-hash path must return a non-nil hash on fetcher miss")
	url := appmedia.BuildTMDBImageURL("w342", path)
	assert.Equal(t, appmedia.HashFromURL(url), *got)
	require.Len(t, lookup.ensureCalls, 1)
	assert.Equal(t, appmedia.HashFromURL(url), lookup.ensureCalls[0].hash)
	assert.Equal(t, url, lookup.ensureCalls[0].sourceURL)
	assert.Equal(t, "poster_w342", lookup.ensureCalls[0].kind)
	require.Len(t, enq.calls, 1, "fallback async enqueue still kicks the pre-warm pipeline")
}

func TestResolver_ResolveSync_ColdMissReturnsEagerHashOnExpiredCtx(t *testing.T) {
	t.Parallel()
	// W19-1b: the sync fetch consumes the caller's posterResolveBudget, so
	// the caller ctx is already expired by the time the eager-hash path
	// runs. EnsurePending + enqueue must still run on a DETACHED, freshly
	// budgeted ctx — otherwise EnsurePending errors (deadline exceeded),
	// the resolver falls to the nil branch, and the cold first view shows a
	// placeholder poster forever. Assert the eager hash comes back non-nil.
	lookup := &fakeMediaLookup{} // EnsurePending succeeds (ensureErr nil)
	enq := &stubEnqueuer{}
	fetcher := &stubFetcher{ok: false} // budget/failure path
	r := NewResolver(lookup, enq, fetcher, silentResolverLogger())
	// Caller ctx is already cancelled — mimics the spent sync budget.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := "/abc.jpg"
	got := r.ResolveSync(ctx, &path, "w342", "poster_w342")
	require.NotNil(t, got, "cold miss on an expired ctx must still return the eager hash (detach worked)")
	url := appmedia.BuildTMDBImageURL("w342", path)
	assert.Equal(t, appmedia.HashFromURL(url), *got)
	require.Len(t, lookup.ensureCalls, 1)
	assert.NoError(t, lookup.lastEnsureErr, "EnsurePending must run on a live (detached) ctx, not the expired caller ctx")
	require.Len(t, enq.calls, 1)
}

func TestResolver_ResolveSync_EagerHashWithoutFetcher(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	path := "/abc.jpg"
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got, "eager-hash path must NOT return nil on miss even without a fetcher")
	url := appmedia.BuildTMDBImageURL("w342", path)
	assert.Equal(t, appmedia.HashFromURL(url), *got)
	require.Len(t, lookup.ensureCalls, 1)
	assert.Equal(t, "poster_w342", lookup.ensureCalls[0].kind)
}

func TestResolver_ResolveSync_EagerHashSkippedOnLookupHit(t *testing.T) {
	t.Parallel()
	path := "/already-warm.jpg"
	url := appmedia.BuildTMDBImageURL("w342", path)
	lookup := &fakeMediaLookup{byURL: map[string]string{url: "already-stored-hash"}}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, "already-stored-hash", *got)
	assert.Empty(t, lookup.ensureCalls, "lookup hit must NOT trigger EnsurePending")
}

func TestResolver_ResolveSync_EagerHashSkippedOnFetcherHit(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	fetcher := &stubFetcher{hash: "warm-hash-from-fetcher", ok: true}
	r := NewResolver(lookup, nil, fetcher, silentResolverLogger())
	path := "/xyz.jpg"
	got := r.ResolveSync(t.Context(), &path, "w1280", "backdrop_w1280")
	require.NotNil(t, got)
	assert.Equal(t, "warm-hash-from-fetcher", *got)
	assert.Empty(t, lookup.ensureCalls, "EnsurePending must NOT fire when FetchSync warmed the row")
}

func TestResolver_ResolveSync_EagerHashFallbackOnEnsureError(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{ensureErr: errors.New("db down")}
	enq := &stubEnqueuer{}
	r := NewResolver(lookup, enq, nil, silentResolverLogger())
	path := "/abc.jpg"
	got := r.ResolveSync(t.Context(), &path, "w342", "poster_w342")
	assert.Nil(t, got, "EnsurePending failure must fall back to nil (legacy behavior)")
	require.Len(t, lookup.ensureCalls, 1)
	require.Len(t, enq.calls, 1, "fallback async enqueue still fires on EnsurePending error")
}

func TestResolver_SetSideEffects_LateBind(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
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

// --- Story 347: uniform always-emit-hash contract ---

func TestResolver_Resolve_FlagOff_LegacyNilOnMiss(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	// Flag explicitly off — preserve pre-347 behavior.
	r.SetUnifiedResolve(false)
	path := "/abc.jpg"
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	assert.Nil(t, got, "flag-off resolve on miss must remain nil (legacy)")
	assert.Empty(t, lookup.ensureCalls, "flag-off must NOT call EnsurePending")
	// Also confirm nil-path stays nil with flag off.
	assert.Nil(t, r.Resolve(t.Context(), nil, "w342", "poster_w342"))
	empty := ""
	assert.Nil(t, r.Resolve(t.Context(), &empty, "w342", "poster_w342"))
}

func TestResolver_Resolve_FlagOn_SentinelOnNilPath(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)
	got := r.Resolve(t.Context(), nil, "w342", "poster_w342")
	require.NotNil(t, got, "flag-on nil path must yield sentinel hash")
	assert.Equal(t, appmedia.SentinelMissingHash, *got)
	empty := ""
	got2 := r.Resolve(t.Context(), &empty, "w342", "poster_w342")
	require.NotNil(t, got2)
	assert.Equal(t, appmedia.SentinelMissingHash, *got2)
	assert.Empty(t, lookup.ensureCalls, "sentinel branch must NOT touch EnsurePending")
}

func TestResolver_Resolve_FlagOn_EagerHashOnLookupMiss(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	enq := &stubEnqueuer{}
	r := NewResolver(lookup, enq, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)
	path := "/abc.jpg"
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got, "flag-on miss must yield eager content hash")
	url := appmedia.BuildTMDBImageURL("w342", path)
	assert.Equal(t, appmedia.HashFromURL(url), *got)
	require.Len(t, lookup.ensureCalls, 1, "EnsurePending must fire once")
	assert.Equal(t, url, lookup.ensureCalls[0].sourceURL)
	assert.Equal(t, "poster_w342", lookup.ensureCalls[0].kind)
	require.Len(t, enq.calls, 1, "async pre-warm enqueue still fires on miss")
}

func TestResolver_Resolve_FlagOn_SentinelOnEnsurePendingFailure(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{ensureErr: errors.New("db down")}
	enq := &stubEnqueuer{}
	r := NewResolver(lookup, enq, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)
	path := "/abc.jpg"
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got, "EnsurePending failure under flag-on must fall back to sentinel, not nil")
	assert.Equal(t, appmedia.SentinelMissingHash, *got)
	require.Len(t, lookup.ensureCalls, 1)
	require.Len(t, enq.calls, 1, "async fallback enqueue still fires")
}

func TestResolver_Resolve_FlagOn_StoredHashStillWins(t *testing.T) {
	t.Parallel()
	const stored = "1111111111111111111111111111111111111111111111111111111111111111"
	path := "/warm.jpg"
	url := appmedia.BuildTMDBImageURL("w342", path)
	lookup := &fakeMediaLookup{byURL: map[string]string{url: stored}}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, stored, *got, "stored hash MUST shadow the eager / sentinel path")
	assert.Empty(t, lookup.ensureCalls, "lookup hit must NOT trigger EnsurePending")
}

func TestResolver_ResolveSync_FlagOn_SentinelOnNilPath(t *testing.T) {
	t.Parallel()
	lookup := &fakeMediaLookup{}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)
	got := r.ResolveSync(t.Context(), nil, "w342", "poster_w342")
	require.NotNil(t, got, "ResolveSync nil path under flag-on must yield sentinel")
	assert.Equal(t, appmedia.SentinelMissingHash, *got)
}

// Regression — the sentinel emission must increment the
// per-reason counter so prod can plot why series tiles render the
// SVG placeholder without trace correlation. Series 288/140/372
// scenario: canon.poster_asset NULL (data backlog from the prior
// merge-policy zeroing bug) → resolver returns sentinel; the
// counter labels the case `reason=nil_path,kind=poster_w342` so an
// operator can grep the metric instead of opening logs.
//
// Not parallel — the counter is process-wide, so two tests
// touching the same reason+kind would race the snapshot reads.
func TestResolver_SentinelEmitCounter_LabelsReason(t *testing.T) {
	lookup := &fakeMediaLookup{}
	r := NewResolver(lookup, nil, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)

	// Snapshot the counters before the call so a re-run inside the
	// same `go test` invocation doesn't double-count.
	nilBefore := sentinelEmitCounter("nil_path", "poster_w342").Get()
	emptyURLBefore := sentinelEmitCounter("empty_url", "poster_w342").Get()

	// nil rawPath → reason=nil_path
	_ = r.Resolve(t.Context(), nil, "w342", "poster_w342")
	if got := sentinelEmitCounter("nil_path", "poster_w342").Get(); got != nilBefore+1 {
		t.Fatalf("nil_path counter: want +1 (%d), got %d", nilBefore+1, got)
	}

	// empty rawPath via ResolveSync also counts as nil_path.
	empty := ""
	_ = r.ResolveSync(t.Context(), &empty, "w342", "poster_w342")
	if got := sentinelEmitCounter("nil_path", "poster_w342").Get(); got != nilBefore+2 {
		t.Fatalf("nil_path counter after empty: want +2 (%d), got %d", nilBefore+2, got)
	}

	// empty-url path: BuildTMDBImageURL returns "" only when raw is
	// whitespace-only; the rawPath != empty guard above short-circuits
	// any literal "". Compose a single-space path to drive the
	// empty_url branch.
	space := " "
	_ = r.Resolve(t.Context(), &space, "w342", "poster_w342")
	if got := sentinelEmitCounter("empty_url", "poster_w342").Get(); got != emptyURLBefore+1 {
		t.Fatalf("empty_url counter: want +1 (%d), got %d", emptyURLBefore+1, got)
	}
}

// Regression — when EnsurePending fails under unified-on, Resolve
// returns the sentinel (per Story 347 fallback) and the counter
// labels the reason `ensure_pending_failed`. This is the third
// reason class operators need split visibility for: data backlog
// (nil_path) vs mapper miss (empty_url) vs persistence pressure
// (ensure_pending_failed) drive different runbooks.
func TestResolver_SentinelEmitCounter_EnsurePendingFailureReason(t *testing.T) {
	lookup := &fakeMediaLookup{ensureErr: errors.New("write_timeout")}
	enq := &stubEnqueuer{}
	r := NewResolver(lookup, enq, nil, silentResolverLogger())
	r.SetUnifiedResolve(true)

	before := sentinelEmitCounter("ensure_pending_failed", "poster_w342").Get()
	path := "/sentinel-on-pending-fail.jpg"
	got := r.Resolve(t.Context(), &path, "w342", "poster_w342")
	require.NotNil(t, got)
	assert.Equal(t, appmedia.SentinelMissingHash, *got)
	if after := sentinelEmitCounter("ensure_pending_failed", "poster_w342").Get(); after != before+1 {
		t.Fatalf("ensure_pending_failed counter: want +1 (%d), got %d", before+1, after)
	}
}
