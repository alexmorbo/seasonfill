package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
	"github.com/alexmorbo/seasonfill/internal/observability"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// defaultDownloaderWorkers is the goroutine count draining the jobs
// channel when DownloaderDeps.Workers is unset. W19-1 lifts this from a
// hardcoded 3 to a BOLD 32: image.tmdb.org is Cloudflare-backed with no
// published per-IP limit, so a wide worker pool clears a cold-series
// media backlog in seconds instead of minutes. Env-tunable for rollback
// via SEASONFILL_MEDIA_DOWNLOADER_WORKERS (threaded through
// DownloaderDeps.Workers by wiring/enrichment.go).
const defaultDownloaderWorkers = 32

// downloadTimeout is the per-request http.Client timeout. 5s matches
// PRD §10.4.8 TMDB API timeout (one TLS-handshake budget).
const downloadTimeout = 10 * time.Second

// retryBackoff is the sleep between the first and second attempt. A
// fixed backoff is fine for an in-process pre-warm — the 5 rps
// limiter already paces overall throughput.
const retryBackoff = 500 * time.Millisecond

// maxBodyBytes is the hard cap on the bytes we read from upstream.
// 32 MiB covers every TMDB image variant (the largest is original
// backdrop ≈ 3 MB).
const maxBodyBytes int64 = 32 << 20

// AssetRepo is the downloader's persistence surface. Production impl
// is *repositories.MediaAssetsRepository (see §5). The narrow port
// keeps the downloader testable with a fake repo.
type AssetRepo interface {
	Get(ctx context.Context, hash string) (media.Asset, error)
	Upsert(ctx context.Context, a media.Asset) error
}

// ErrAssetNotFound is the sentinel the repository returns on Get
// miss. Mirrors application/ports.ErrNotFound but kept in the media
// package so the downloader has zero downstream-port imports.
var ErrAssetNotFound = errors.New("media: asset not found")

// Downloader is the consumer side of the pre-warm pipeline. Three
// goroutines drain the jobs channel; each goroutine waits against
// the shared rate limiter before issuing the upstream GET. Built
// against the Enqueuer's channel + dedup set so completion clears
// the in-flight mark.
type Downloader struct {
	jobs       <-chan job
	dedup      *inflightSet
	store      mediastore.Store
	repo       AssetRepo
	http       *http.Client
	limiter    *rate.Limiter
	logger     *slog.Logger
	clock      func() time.Time
	workers    int
	statBudget time.Duration
	wg         sync.WaitGroup
	stopOnce   sync.Once
	stopCh     chan struct{}
}

// DownloaderDeps is the explicit dep bundle. http=nil falls back to
// http.DefaultClient — but production wiring MUST pass the TMDB
// proxied client (see §7 and the story's TMDB proxy decision).
//
// Workers is the drain-goroutine count; 0 → defaultDownloaderWorkers (32).
// Threaded from SEASONFILL_MEDIA_DOWNLOADER_WORKERS (W19-1).
//
// CDNRateLimitRPS is the image-CDN rps cap (image.tmdb.org). <=0 →
// UNCAPPED (rate.Inf) as of W19-1 — the CDN has no per-IP limit and the
// old 100 rps self-throttle stalled cold fills. A positive value
// re-imposes a finite cap (rollback via SEASONFILL_TMDB_CDN_RPS). The
// on-demand fetcher shares this limiter, so the cap (or lack of it)
// applies to both sync + async paths.
type DownloaderDeps struct {
	Store           mediastore.Store
	Repo            AssetRepo
	HTTPClient      *http.Client
	Logger          *slog.Logger
	Clock           func() time.Time
	Workers         int
	CDNRateLimitRPS float64
	// StatBudget bounds the async pre-warm S3 Stat probe with a short
	// sub-context so a slow HEAD on a MISSING object (~6-21s on SeaweedFS,
	// minio-go retries) doesn't pin a worker. 0 → onDemandStatBudget
	// (800ms). W19-3b: wired from the SAME SEASONFILL_MEDIA_STAT_BUDGET as
	// the on-demand fetcher (one env drives both paths).
	StatBudget time.Duration
}

// NewDownloader wires the consumer against the Enqueuer's channel +
// dedup set. Start launches the goroutines; Close drains and waits.
func NewDownloader(eq *Enqueuer, deps DownloaderDeps) (*Downloader, error) {
	if eq == nil {
		return nil, errors.New("media downloader: enqueuer required")
	}
	if deps.Store == nil {
		return nil, errors.New("media downloader: mediastore required")
	}
	if deps.Repo == nil {
		return nil, errors.New("media downloader: asset repo required")
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = &http.Client{Timeout: downloadTimeout}
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	workers := deps.Workers
	if workers <= 0 {
		workers = defaultDownloaderWorkers
	}
	// W19-3b: bound the pre-warm Stat with the SAME 800ms sub-budget the
	// on-demand fetcher uses (onDemandStatBudget) so a slow/missing HEAD
	// falls through to the download path instead of pinning a worker.
	statBudget := deps.StatBudget
	if statBudget <= 0 {
		statBudget = onDemandStatBudget
	}

	// W19-1: <=0 rps → uncapped. image.tmdb.org is Cloudflare-backed with
	// no published per-IP budget; rate.Inf never blocks Wait(). A positive
	// value re-imposes a finite steady-rate cap (burst=1 keeps the exact
	// pacing the rollback cap is meant to enforce).
	var limiter *rate.Limiter
	if deps.CDNRateLimitRPS <= 0 {
		limiter = rate.NewLimiter(rate.Inf, 1)
	} else {
		limiter = rate.NewLimiter(rate.Limit(deps.CDNRateLimitRPS), 1)
	}

	return &Downloader{
		jobs:       eq.Channel(),
		dedup:      eq.dedup,
		store:      deps.Store,
		repo:       deps.Repo,
		http:       deps.HTTPClient,
		limiter:    limiter,
		logger:     deps.Logger,
		clock:      deps.Clock,
		workers:    workers,
		statBudget: statBudget,
		stopCh:     make(chan struct{}),
	}, nil
}

// Start spawns the worker goroutines. Idempotent against a re-call
// (the wg.Add path is guarded by the closed stopCh).
func (d *Downloader) Start(ctx context.Context) {
	for i := range d.workers {
		d.wg.Add(1)
		go d.runWorker(ctx, i)
	}
}

// Close blocks until every drained job completes. Called from the
// shutdown path; expects the Enqueuer to have already been Closed
// (closing eq.jobs is what terminates the worker select).
func (d *Downloader) Close() {
	d.stopOnce.Do(func() { close(d.stopCh) })
	d.wg.Wait()
}

// Limiter returns the shared *rate.Limiter the Downloader uses. The
// on-demand fetcher (Story 316) must share the limiter so the CDN cap
// applies across both the sync + async paths. W19-1: uncapped by default;
// SEASONFILL_TMDB_CDN_RPS re-imposes a finite cap.
func (d *Downloader) Limiter() *rate.Limiter { return d.limiter }

func (d *Downloader) runWorker(ctx context.Context, idx int) {
	defer d.wg.Done()
	log := d.logger.With(slog.Int("worker_idx", idx))
	for {
		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		case j, ok := <-d.jobs:
			if !ok {
				return
			}
			d.handle(ctx, log, j)
		}
	}
}

// handle is the per-job state machine. Always clears the dedup mark
// before returning so a future pre-warm for the same hash can land.
func (d *Downloader) handle(ctx context.Context, log *slog.Logger, j job) {
	defer d.dedup.remove(j.Hash)

	start := d.clock()
	key := mediastore.Key(j.UpstreamURL, j.Extension)
	jlog := log.With(
		slog.String("hash", j.Hash),
		slog.String("kind", j.Kind),
		slog.String("upstream_url", j.UpstreamURL),
		slog.String("key", key),
	)

	jlog.InfoContext(ctx, "media.fetch.start",
		slog.String("source_url", j.UpstreamURL),
		slog.String("hash", j.Hash),
		slog.String("kind", j.Kind),
	)

	// 1. Stat short-circuit: object already in the store. We still
	//    want to ensure the media_assets row exists with
	//    status=stored (the row could be missing after a failed
	//    deploy that bypassed the row write).
	// W19-3b: bound the Stat with a short sub-context. A HEAD on a MISSING
	// object is pathologically slow on SeaweedFS (~6-21s: minio-go retries
	// the non-clean miss with backoff), which would pin a worker. Treating
	// a timed-out HEAD as a miss lets the job fall through to the download
	// path quickly. statCancel MUST always run before the branches.
	statCtx, statCancel := context.WithTimeout(ctx, d.statBudget)
	info, err := d.store.Stat(statCtx, key)
	statCancel()
	switch {
	case err == nil:
		_ = d.upsertRow(ctx, media.Asset{
			Hash:        j.Hash,
			UpstreamURL: j.UpstreamURL,
			Kind:        j.Kind,
			ContentType: info.ContentType,
			Size:        info.Size,
			Status:      media.StatusStored,
		}, jlog)
		jlog.DebugContext(ctx, "media.prewarm.stat_hit",
			slog.Int("duration_ms", int(d.clock().Sub(start).Milliseconds())),
		)
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		// The short stat sub-budget expired (or ctx cancelled). A slow HEAD
		// on a MISSING object looks exactly like this, so treat it as a
		// store miss and FALL THROUGH to the download path — do NOT return
		// early and do NOT escalate to the stat_error warn below.
		jlog.DebugContext(ctx, "media.prewarm.stat_skip",
			slog.Int("stat_ms", int(d.clock().Sub(start).Milliseconds())))
	case !errors.Is(err, mediastore.ErrNotFound) && !errors.Is(err, mediastore.ErrNotSupported):
		// Stat returned an unexpected error — log + fall through to
		// the download path; the upstream fetch is the source of
		// truth and a successful Put will mask the Stat blip.
		jlog.WarnContext(ctx, "media.prewarm.stat_error",
			slog.String("error", err.Error()))
	}

	// 2. Initial row write — status=pending — so a concurrent
	//    handler GET for this hash returns 404 (frontend
	//    placeholder) instead of "row missing" → 404.
	if err := d.upsertRow(ctx, media.Asset{
		Hash:        j.Hash,
		UpstreamURL: j.UpstreamURL,
		Kind:        j.Kind,
		Status:      media.StatusPending,
	}, jlog); err != nil {
		// Row write failed — give up. The next pre-warm pass will
		// retry.
		return
	}

	// 3. Download with retry. transientError reports whether to retry
	//    (network / 5xx) vs give up (4xx other than 429).
	body, contentType, attempt, lastErr := d.downloadWithRetry(ctx, jlog, j.UpstreamURL)
	if lastErr != nil {
		// All attempts exhausted. Persist failed + log.
		_ = d.upsertRow(ctx, media.Asset{
			Hash:        j.Hash,
			UpstreamURL: j.UpstreamURL,
			Kind:        j.Kind,
			Status:      media.StatusFailed,
		}, jlog)
		kind := ClassifyFetchError(lastErr)
		jlog.WarnContext(ctx, "media.fetch.failed",
			slog.String("source_url", j.UpstreamURL),
			slog.String("hash", j.Hash),
			slog.String("kind", j.Kind),
			slog.String("error_kind", string(kind)),
			slog.Int("http_status", HTTPStatus(lastErr)),
			slog.Int("attempts", attempt),
			slog.Int("duration_ms", int(d.clock().Sub(start).Milliseconds())),
			slog.String("error", lastErr.Error()),
		)
		observability.IncMediaFetch("failed", string(kind))
		return
	}

	// 4. Put bytes to the store. Failed Put is sticky → status=failed.
	if err := d.store.Put(ctx, key, bytesReader(body), int64(len(body)), contentType); err != nil {
		_ = d.upsertRow(ctx, media.Asset{
			Hash:        j.Hash,
			UpstreamURL: j.UpstreamURL,
			Kind:        j.Kind,
			Status:      media.StatusFailed,
		}, jlog)
		jlog.ErrorContext(ctx, "media.fetch.failed",
			slog.String("source_url", j.UpstreamURL),
			slog.String("hash", j.Hash),
			slog.String("kind", j.Kind),
			slog.String("error_kind", string(ErrorKindS3Write)),
			slog.Int("http_status", 0),
			slog.Int("size_bytes", len(body)),
			slog.String("error", err.Error()),
		)
		observability.IncMediaFetch("failed", string(ErrorKindS3Write))
		return
	}

	// 5. Final row write — status=stored, authoritative size +
	//    content-type.
	if err := d.upsertRow(ctx, media.Asset{
		Hash:        j.Hash,
		UpstreamURL: j.UpstreamURL,
		Kind:        j.Kind,
		ContentType: contentType,
		Size:        int64(len(body)),
		Status:      media.StatusStored,
	}, jlog); err != nil {
		// The bytes are in the store but the row write failed — the
		// handler's lost-object recovery path will not fire (it
		// looks at the row, not the store). The next pre-warm pass
		// will retry the row write.
		return
	}

	jlog.InfoContext(ctx, "media.fetch.ok",
		slog.String("source_url", j.UpstreamURL),
		slog.String("hash", j.Hash),
		slog.String("kind", j.Kind),
		slog.Int("attempts", attempt),
		slog.Int("size_bytes", len(body)),
		slog.String("content_type", contentType),
		slog.Int("duration_ms", int(d.clock().Sub(start).Milliseconds())),
	)
	observability.IncMediaFetch("ok", "")
}

// downloadWithRetry does one or two HTTP GETs against url and
// returns the body + content-type. Returns (nil, "", attempts, err)
// when both attempts fail. waits on the rate limiter before each
// attempt.
func (d *Downloader) downloadWithRetry(ctx context.Context, log *slog.Logger, url string) ([]byte, string, int, error) {
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if err := d.limiter.Wait(ctx); err != nil {
			return nil, "", attempt, fmt.Errorf("rate wait: %w", err)
		}
		body, ct, transient, err := d.fetchOnce(ctx, url)
		if err == nil {
			return body, ct, attempt, nil
		}
		lastErr = err
		if !transient {
			return nil, "", attempt, err
		}
		log.DebugContext(ctx, "media.prewarm.retry",
			slog.Int("attempt", attempt),
			slog.String("error", err.Error()),
		)
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, "", attempt, ctx.Err()
			case <-time.After(retryBackoff):
			}
		}
	}
	return nil, "", 2, lastErr
}

// fetchOnce does one HTTP GET. transient is true for retryable
// failures (network errors, 429, 5xx); false for terminal (4xx
// non-429). The cap on body bytes is enforced via io.LimitReader.
func (d *Downloader) fetchOnce(ctx context.Context, url string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("new request: %w", err)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, "", true, fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, "", true, newHTTPStatusError(resp.StatusCode, req.URL.String())
	}
	if resp.StatusCode >= 400 {
		return nil, "", false, newHTTPStatusError(resp.StatusCode, req.URL.String())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, "", true, fmt.Errorf("read body: %w", err)
	}
	return body, resp.Header.Get("Content-Type"), false, nil
}

func (d *Downloader) upsertRow(ctx context.Context, a media.Asset, log *slog.Logger) error {
	if err := a.Validate(); err != nil {
		log.WarnContext(ctx, "media.prewarm.row_invalid",
			slog.String("error", err.Error()))
		return err
	}
	if err := d.repo.Upsert(ctx, a); err != nil {
		log.WarnContext(ctx, "media.fetch.failed",
			slog.String("source_url", a.UpstreamURL),
			slog.String("hash", a.Hash),
			slog.String("kind", a.Kind),
			slog.String("error_kind", string(ErrorKindDBWrite)),
			slog.Int("http_status", 0),
			slog.String("error", err.Error()))
		observability.IncMediaFetch("failed", string(ErrorKindDBWrite))
		return err
	}
	return nil
}

// bytesReader is io.Reader over a []byte. Inlined instead of
// bytes.NewReader to avoid the unused import for that single use; a
// trivial wrapper is fine here.
type byteReader struct {
	b []byte
	i int
}

func bytesReader(b []byte) io.Reader { return &byteReader{b: b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
