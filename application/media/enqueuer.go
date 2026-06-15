// Package media owns the application-level pre-warm pipeline. Enqueuer
// is the producer side; Downloader (downloader.go) is the consumer
// side. The two share an unbuffered control plane (a channel of
// EnqueueRequest values) so backpressure is explicit — a full channel
// causes the enqueuer's Enqueue call to drop with a logged warning
// rather than block the series_worker.
//
// Lifecycle: built by cmd/server.wireEnrichment → handed to the
// series worker as a MediaPrewarmer port via SeriesWorkerDeps →
// invoked AFTER the tx commits with the slice of EnqueueRequest
// values computed from the mapped TMDB payload.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
)

// tmdbImageBase is the canonical TMDB image CDN host. Story 211's
// mapper emits paths like "/abc.jpg"; F-1 stamps the base + size
// variant onto each path to produce the full UpstreamURL.
const tmdbImageBase = "https://image.tmdb.org/t/p"

// TMDBImageBase is the exported CDN host+/t/p prefix so SQL projections
// (story 348a series_cache LEFT JOIN media_assets) can mint the
// source_url for batched hash lookup. Stable across the lifetime of
// TMDB v3.
const TMDBImageBase = tmdbImageBase

// SeriesPosterListSize is the canonical w342 hero-poster size used by
// the catalog tiles. MUST match what composer.resolveAssets passes to
// MediaResolver and what the prewarm pipeline writes for the series
// list — handlers derive the wire `poster_hash` by hashing
// BuildTMDBImageURL(SeriesPosterListSize, s.poster_asset).
const SeriesPosterListSize = "w342"

// channelCap is the pre-warm queue depth — PRD §6.6. Sized for ~10
// series worth of pre-warm in flight (each series produces ~30 assets
// = poster ×2, backdrop, network logos, top-10 profiles, season
// posters, trailer thumb). When the channel is full the enqueuer
// drops new requests with a "queue_full" warn log rather than
// blocking the series worker.
const channelCap = 500

// EnqueueRequest is the producer-side input shape. UpstreamURL is the
// fully-qualified canonical URL (the enqueuer hashes it to derive the
// content-addressed hash). Kind is the descriptive label per PRD §6.4
// ("poster_w342" / "backdrop_w1280" / etc.) — stored on the
// media_assets row for the future GC sweep. Extension is the file
// suffix ("jpg" / "png") used by the mediastore key builder; the
// downloader normalizes it but the producer is the source of truth
// because the mapper knows the upstream filename.
type EnqueueRequest struct {
	UpstreamURL string
	Kind        string
	Extension   string
}

// Enqueuer is the SeriesWorkerDeps.MediaPrewarmer-compatible producer.
// Built once at boot; safe for concurrent use. dedup tracks hashes
// currently in flight (or recently enqueued) so the same upstream URL
// requested twice during one series upsert produces ONE downloader
// job. The dedup set is cleared by the downloader when the job
// terminates (success or failure).
type Enqueuer struct {
	jobs   chan job
	dedup  *inflightSet
	logger *slog.Logger
}

// job is the downloader's internal work item. Hash + Extension are
// pre-computed (the producer already paid the sha256 cost), so the
// downloader does not redo it.
type job struct {
	Hash        string
	UpstreamURL string
	Kind        string
	Extension   string
}

// NewEnqueuer constructs the producer. logger=nil falls back to
// slog.Default. The downloader is constructed against the SAME jobs
// channel + dedup set via NewDownloader(eq.Channel(), eq.Dedup(), ...).
func NewEnqueuer(logger *slog.Logger) *Enqueuer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Enqueuer{
		jobs:   make(chan job, channelCap),
		dedup:  newInflightSet(),
		logger: logger,
	}
}

// Enqueue is the MediaPrewarmer port satisfaction. Translates each
// EnqueueRequest to an internal job, dedups by hash, and submits to
// the channel. The legacy SeriesWorkerDeps.MediaPrewarmer port shape
// is `Enqueue(ctx, []string)` — the wiring layer (Story 211 left
// this nil) is widened in §6 to pass []EnqueueRequest; see §6 for
// the port-shape change.
func (e *Enqueuer) Enqueue(ctx context.Context, reqs []EnqueueRequest) {
	if e == nil || len(reqs) == 0 {
		return
	}
	for _, r := range reqs {
		clean := strings.TrimSpace(r.UpstreamURL)
		if clean == "" {
			continue
		}
		hash := hashURL(clean)
		if !e.dedup.tryAdd(hash) {
			// Already in flight — skip silently. The downloader's
			// completion callback clears the dedup entry, so a future
			// pre-warm for the same hash succeeds.
			continue
		}
		j := job{
			Hash:        hash,
			UpstreamURL: clean,
			Kind:        r.Kind,
			Extension:   normaliseExt(r.Extension),
		}
		select {
		case e.jobs <- j:
		default:
			// Channel full — drop. Removes the dedup mark so a
			// retry on the next pre-warm pass can still land.
			e.dedup.remove(hash)
			e.logger.WarnContext(ctx, "media.prewarm.queue_full",
				slog.String("hash", hash),
				slog.String("kind", r.Kind),
				slog.String("upstream_url", clean),
			)
		}
	}
}

// Channel returns the consumer side. Used ONLY by NewDownloader to
// wire the producer to the consumer; nothing else may touch it.
func (e *Enqueuer) Channel() <-chan job { return e.jobs }

// Close drains-and-stops the channel. Called from the shutdown path
// in main.go; idempotent.
func (e *Enqueuer) Close() {
	e.dedup.closeAll()
	// Closing the channel signals downloader goroutines to drain +
	// exit. A nil-recv guard in the loop body handles the closed
	// channel case.
	close(e.jobs)
}

// inflight is a simple set-with-mutex; sync.Map would also work but
// the API surface (tryAdd / remove) is cleaner explicitly.
type inflightSet struct {
	mu  sync.Mutex
	set map[string]struct{}
}

func newInflightSet() *inflightSet { return &inflightSet{set: make(map[string]struct{}, 64)} }

func (s *inflightSet) tryAdd(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.set[hash]; exists {
		return false
	}
	s.set[hash] = struct{}{}
	return true
}

func (s *inflightSet) remove(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.set, hash)
}

func (s *inflightSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set = map[string]struct{}{}
}

// hashURL returns the lowercase sha256-hex of url. Exported via
// BuildTMDBImageURL + HashFromURL so the series worker can pre-hash
// for pre-warm assertions in tests.
func hashURL(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// HashFromURL is the external surface for callers that need to mint
// the same hash the enqueuer would. Used by series_worker_test +
// media_test.
func HashFromURL(url string) string { return hashURL(strings.TrimSpace(url)) }

// sentinelMissingHashSeed is the salted input the sentinel sha256
// hashes. MUST NOT be a valid TMDB CDN URL prefix so collisions with
// content-addressed hashes are impossible — every real upstream URL
// hashed by HashFromURL begins with "https://image.tmdb.org/t/p/...".
const sentinelMissingHashSeed = "seasonfill:media:sentinel:missing:v1"

// SentinelMissingHash is the deterministic sha256-hex of
// sentinelMissingHashSeed. Story 347 — composer / resolver hand this
// value to the frontend in place of nil when an asset has no raw path
// (or no recoverable source URL). The media handler short-circuits on
// it and serves the embedded SVG placeholder without a DB lookup.
//
// Stable across processes — literally sha256("seasonfill:media:
// sentinel:missing:v1") in lowercase hex. var (not const) because
// crypto/sha256 is not const-eligible in Go; computed once at package
// init.
var SentinelMissingHash = func() string {
	sum := sha256.Sum256([]byte(sentinelMissingHashSeed))
	return hex.EncodeToString(sum[:])
}()

// BuildTMDBImageURL stamps the TMDB CDN base + size onto a raw image
// path emitted by the mapper. path is the value of poster_path /
// backdrop_path / etc. — the leading slash is preserved. Returns
// empty when path is empty (caller skips the enqueue).
func BuildTMDBImageURL(size, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return tmdbImageBase + "/" + size + path
}

// normaliseExt extracts the lowercase extension from a TMDB image
// path or an empty string from the producer. TMDB serves both .jpg
// and .png — strip leading dot, lowercase, return "" for unknown.
func normaliseExt(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	switch ext {
	case "jpg", "jpeg", "png", "webp", "gif":
		return ext
	}
	return ""
}

// ExtractExt is the public helper for callers that have a path
// rather than an extension (e.g. "/abc.jpg" → "jpg").
func ExtractExt(path string) string {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 || dot == len(path)-1 {
		return ""
	}
	return normaliseExt(path[dot+1:])
}
