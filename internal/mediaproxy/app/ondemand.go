// Package media — Story 316 on-demand fetcher.
//
// FetchSync is the synchronous variant of Downloader.handle: it
// fetches a single TMDB-style asset, puts the bytes to the store,
// upserts the media_assets row, returns the sha256 hash. Designed
// for the page-composer "first-fold" path (hero poster/backdrop, person
// portrait) so the visited assets land in S3 before the response goes
// out. Cap'd by the shared CDN rate limiter, which is uncapped by default
// (W19-1); SEASONFILL_TMDB_CDN_RPS re-imposes a finite cap so a hot page
// can't hammer TMDB.
//
// Failures and ctx-cancel return ("", false) — the resolver
// translates that to nil and the frontend falls back to a monogram.
// The async pre-warm pipeline (Enqueuer + Downloader) is unchanged
// and remains the bulk-fill path for assets not on the first-fold.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
	"github.com/alexmorbo/seasonfill/internal/observability"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// onDemandTimeout is the DEFAULT floor applied to a single FetchSync
// invocation when OnDemandDeps.Timeout is unset. W19-1 lifts it 1.5s →
// 10s so the effective on-demand budget actually reaches 10s (the old
// 1.5s floor silently capped the handler's larger wall budget — see the
// contextWithFloorTimeout CEILING semantics). Production wires this from
// SEASONFILL_MEDIA_ONDEMAND_BUDGET via OnDemandDeps.Timeout.
const onDemandTimeout = 10 * time.Second

// onDemandStatBudget is the DEFAULT short sub-budget for the S3 Stat
// probe when OnDemandDeps.StatBudget is unset (W19-3a). A HEAD on a
// MISSING object is pathologically slow on SeaweedFS (~21s: the missing
// object returns non-cleanly → minio-go retries with backoff), while a
// HEAD on an existing object is ~5ms. Bounding the Stat with this short
// sub-context lets a cold poster fall through to the fetch path quickly
// instead of burning the whole on-demand budget on a doomed HEAD.
const onDemandStatBudget = 800 * time.Millisecond

// negativeCacheTTL bounds how long a hash stays in the failed-fetch
// cooldown. The short window matches the fast W19-1 downloader: a
// pending/failed hash typically heals in seconds, so a long cooldown
// would needlessly keep serving the placeholder instead of retrying
// the now-stored bytes. 20s still throttles a genuinely-dead upstream
// URL from being re-hammered on every request.
const negativeCacheTTL = 20 * time.Second

// OnDemandFetcher is the public interface; *onDemandFetcher is the
// production impl. Kept as an interface so the composer test stubs
// can verify the call shape without touching the downloader's wire.
type OnDemandFetcher interface {
	FetchSync(ctx context.Context, upstreamURL, kind, ext string) (string, bool)
}

// OnDemandDeps groups the prod wiring. Mirrors DownloaderDeps so the
// wiring code can hand the same Store / Repo / HTTPClient. Limiter
// MUST be the shared *rate.Limiter the Downloader uses (otherwise the
// CDN cap gets split — uncapped by default as of W19-1).
type OnDemandDeps struct {
	Store      mediastore.Store
	Repo       AssetRepo
	HTTPClient *http.Client
	Limiter    *rate.Limiter
	// Timeout is the per-fetch floor (and default http.Client.Timeout).
	// 0 → onDemandTimeout (10s). W19-1: wired from
	// SEASONFILL_MEDIA_ONDEMAND_BUDGET so the fetcher floor tracks the
	// handler wall budget and the two never drift apart.
	Timeout time.Duration
	// StatBudget bounds the S3 Stat probe with a short sub-context so a
	// slow HEAD on a missing object doesn't consume the whole on-demand
	// budget. 0 → onDemandStatBudget (800ms). W19-3a: wired from
	// SEASONFILL_MEDIA_STAT_BUDGET.
	StatBudget time.Duration
	Logger     *slog.Logger
	Clock      func() time.Time
}

// onDemandFetcher is the production OnDemandFetcher.
type onDemandFetcher struct {
	store      mediastore.Store
	repo       AssetRepo
	http       *http.Client
	limiter    *rate.Limiter
	logger     *slog.Logger
	clock      func() time.Time
	timeout    time.Duration
	statBudget time.Duration

	negMu    sync.Mutex
	negUntil map[string]time.Time // asset hash -> retry-after (cooldown)
}

// NewOnDemandFetcher constructs the prod fetcher. The shared Limiter
// (the same *rate.Limiter the Downloader holds) is required to keep
// the global CDN cap intact across sync + async paths (uncapped by
// default as of W19-1).
func NewOnDemandFetcher(d OnDemandDeps) (OnDemandFetcher, error) {
	if d.Store == nil {
		return nil, errors.New("media ondemand: mediastore required")
	}
	if d.Repo == nil {
		return nil, errors.New("media ondemand: asset repo required")
	}
	if d.Limiter == nil {
		return nil, errors.New("media ondemand: limiter required (must share with downloader)")
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = onDemandTimeout
	}
	statBudget := d.StatBudget
	if statBudget <= 0 {
		statBudget = onDemandStatBudget
	}
	if d.HTTPClient == nil {
		d.HTTPClient = &http.Client{Timeout: timeout}
	}
	if d.Logger == nil {
		d.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if d.Clock == nil {
		d.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &onDemandFetcher{
		store:      d.Store,
		repo:       d.Repo,
		http:       d.HTTPClient,
		limiter:    d.Limiter,
		logger:     d.Logger,
		clock:      d.Clock,
		timeout:    timeout,
		statBudget: statBudget,
		negUntil:   make(map[string]time.Time),
	}, nil
}

// inCooldown reports whether hash is inside its failed-fetch cooldown.
func (f *onDemandFetcher) inCooldown(hash string) bool {
	f.negMu.Lock()
	defer f.negMu.Unlock()
	until, ok := f.negUntil[hash]
	return ok && f.clock().Before(until)
}

// markFailed puts hash into cooldown for negativeCacheTTL and prunes
// any already-expired entries so the map can't grow unbounded.
func (f *onDemandFetcher) markFailed(hash string) {
	now := f.clock()
	f.negMu.Lock()
	defer f.negMu.Unlock()
	for h, until := range f.negUntil {
		if !now.Before(until) {
			delete(f.negUntil, h)
		}
	}
	f.negUntil[hash] = now.Add(negativeCacheTTL)
	// M-5: Set under negMu so the len() read is consistent with the mutation (-race safe).
	observability.SetMediaOnDemandCooldownSize(len(f.negUntil))
}

// clearCooldown drops hash from the negative cache after a success so
// it heals immediately (no waiting out the TTL).
func (f *onDemandFetcher) clearCooldown(hash string) {
	f.negMu.Lock()
	defer f.negMu.Unlock()
	delete(f.negUntil, hash)
	// M-5: Set under negMu so the len() read is consistent with the mutation (-race safe).
	observability.SetMediaOnDemandCooldownSize(len(f.negUntil))
}

// FetchSync is the synchronous fetch. Returns (hash, true) on success;
// ("", false) on timeout / failure. NEVER returns an error — the
// composer never wants to surface fetch failure to the response; it
// just renders the monogram fallback.
func (f *onDemandFetcher) FetchSync(ctx context.Context, upstreamURL, kind, ext string) (string, bool) {
	clean := strings.TrimSpace(upstreamURL)
	if clean == "" {
		return "", false
	}
	hash := HashFromURL(clean)
	ext = normaliseExt(ext)

	// M-5: coarse per-FetchSync outcome. Default "fail"; the success + cooldown
	// paths override before their return. One deferred emit = exactly once per
	// real attempt (the empty-URL guard above is intentionally excluded).
	result := "fail"
	defer func() { observability.IncMediaOnDemand(result) }()

	// Inherit caller deadline; if none, apply onDemandTimeout as a hard
	// floor so the composer can't accidentally hold the response.
	ctx, cancel := contextWithFloorTimeout(ctx, f.timeout)
	defer cancel()

	start := f.clock()
	log := f.logger.With(
		slog.String("hash", hash),
		slog.String("kind", kind),
		slog.String("source_url", clean),
	)
	log.InfoContext(ctx, "media.ondemand.start",
		slog.Int("deadline_ms", deadlineMillis(ctx, f.clock())),
	)

	key := mediastore.Key(clean, ext)

	// 1. Stat short-circuit. If the row is already in store, just upsert
	// the row + return the hash.
	statStart := f.clock()
	statCtx, statCancel := context.WithTimeout(ctx, f.statBudget)
	info, statErr := f.store.Stat(statCtx, key)
	statCancel()
	statMS := int(f.clock().Sub(statStart).Milliseconds())
	if statErr == nil {
		_ = f.upsert(ctx, media.Asset{
			Hash:        hash,
			UpstreamURL: clean,
			Kind:        kind,
			ContentType: info.ContentType,
			Size:        info.Size,
			Status:      media.StatusStored,
		}, log)
		log.DebugContext(ctx, "media.ondemand.stat_hit",
			slog.Int("stat_ms", statMS),
			slog.Int("duration_ms", int(f.clock().Sub(start).Milliseconds())))
		f.clearCooldown(hash)
		result = "success"
		return hash, true
	}
	// A Stat that timed out needs care: errors.Is(…, DeadlineExceeded)
	// fires for BOTH the short stat sub-budget (parent still alive) AND
	// genuine parent exhaustion, so inspect the PARENT ctx to tell them
	// apart (W19-3a).
	if errors.Is(statErr, context.DeadlineExceeded) || errors.Is(statErr, context.Canceled) {
		if ctx.Err() != nil {
			// Parent budget is gone — no time left to fetch. Keep the
			// W19-M behaviour: return with stage="s3_stat" instead of
			// falling through to limiter.Wait, which would mislabel a
			// stat-consumed deadline as stage="rate_wait".
			log.WarnContext(ctx, "media.ondemand.timeout",
				slog.String("stage", "s3_stat"),
				slog.Int("stat_ms", statMS))
			f.markFailed(hash)
			return "", false
		}
		// Only the short stat sub-budget expired; the parent still has
		// budget. A slow HEAD on a MISSING object looks exactly like this
		// (~21s on SeaweedFS), so treat it as a store miss and FALL
		// THROUGH to the fetch path so the cold poster still fills.
		log.DebugContext(ctx, "media.ondemand.stat_skip",
			slog.Int("stat_ms", statMS))
	} else if !errors.Is(statErr, mediastore.ErrNotFound) && !errors.Is(statErr, mediastore.ErrNotSupported) {
		log.WarnContext(ctx, "media.ondemand.stat_error",
			slog.Int("stat_ms", statMS),
			slog.String("error", statErr.Error()))
		// Fall through — upstream fetch is the source of truth.
	}

	// Cooldown gate — placed AFTER the stat short-circuit so an
	// already-stored (or async-Downloader-filled) asset always serves and
	// heals immediately; the cooldown only bounds the expensive upstream
	// path (limiter wait + TMDB GET + S3 Put) while a store/image is broken.
	if f.inCooldown(hash) {
		observability.IncMediaFetch("skipped", "cooldown")
		result = "cooldown_short_circuit"
		log.DebugContext(ctx, "media.ondemand.skipped",
			slog.String("reason", "cooldown"))
		return "", false
	}

	// 2. Wait on the shared rate limiter. Past-deadline → bail.
	if err := f.limiter.Wait(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			log.WarnContext(ctx, "media.ondemand.timeout",
				slog.String("stage", "rate_wait"))
		} else {
			log.WarnContext(ctx, "media.ondemand.failed",
				slog.String("error_kind", "rate_wait_error"),
				slog.String("error", err.Error()))
			observability.IncMediaFetch("failed", string(ErrorKindRateWait))
		}
		f.markFailed(hash)
		return "", false
	}

	// 3. HTTP GET.
	fetchStart := f.clock()
	body, contentType, err := f.fetchOnce(ctx, clean)
	fetchMS := int(f.clock().Sub(fetchStart).Milliseconds())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			log.WarnContext(ctx, "media.ondemand.timeout",
				slog.String("stage", "http"),
				slog.Int("stat_ms", statMS),
				slog.Int("fetch_ms", fetchMS))
			f.markFailed(hash)
			return "", false
		}
		log.WarnContext(ctx, "media.ondemand.failed",
			slog.String("error_kind", string(ClassifyFetchError(err))),
			slog.Int("http_status", HTTPStatus(err)),
			slog.Int("fetch_ms", fetchMS),
			slog.String("error", err.Error()))
		observability.IncMediaFetch("failed", string(ClassifyFetchError(err)))
		f.markFailed(hash)
		return "", false
	}

	// 4. Put bytes to store.
	putStart := f.clock()
	putErr := f.store.Put(ctx, key, bytes.NewReader(body), int64(len(body)), contentType)
	putMS := int(f.clock().Sub(putStart).Milliseconds())
	if putErr != nil {
		log.ErrorContext(ctx, "media.ondemand.failed",
			slog.String("error_kind", string(ErrorKindS3Write)),
			slog.Int("put_ms", putMS),
			slog.String("error", putErr.Error()))
		observability.IncMediaFetch("failed", string(ErrorKindS3Write))
		f.markFailed(hash)
		return "", false
	}

	// 5. Final row write — status=stored.
	if err := f.upsert(ctx, media.Asset{
		Hash:        hash,
		UpstreamURL: clean,
		Kind:        kind,
		ContentType: contentType,
		Size:        int64(len(body)),
		Status:      media.StatusStored,
	}, log); err != nil {
		// Bytes are in store but row write failed; subsequent reads
		// can recover via the media handler's lost-object path.
		// Treat as a partial success.
		f.markFailed(hash)
		return "", false
	}

	log.InfoContext(ctx, "media.ondemand.ok",
		slog.Int("size_bytes", len(body)),
		slog.String("content_type", contentType),
		slog.Int("stat_ms", statMS),
		slog.Int("fetch_ms", fetchMS),
		slog.Int("put_ms", putMS),
		slog.Int("duration_ms", int(f.clock().Sub(start).Milliseconds())),
	)
	observability.IncMediaFetch("ok", "")
	f.clearCooldown(hash)
	result = "success"
	return hash, true
}

// fetchOnce — one HTTP GET, bounded by maxBodyBytes. Errors are returned
// raw so ClassifyFetchError can categorise them.
func (f *onDemandFetcher) fetchOnce(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("new request: %w", err)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, "", newHTTPStatusError(resp.StatusCode, req.URL.String())
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (f *onDemandFetcher) upsert(ctx context.Context, a media.Asset, log *slog.Logger) error {
	if err := a.Validate(); err != nil {
		log.WarnContext(ctx, "media.ondemand.row_invalid",
			slog.String("error", err.Error()))
		return err
	}
	if err := f.repo.Upsert(ctx, a); err != nil {
		log.WarnContext(ctx, "media.ondemand.failed",
			slog.String("error_kind", string(ErrorKindDBWrite)),
			slog.String("error", err.Error()))
		observability.IncMediaFetch("failed", string(ErrorKindDBWrite))
		return err
	}
	return nil
}

// contextWithFloorTimeout returns a child context whose deadline is the
// MINIMUM of the parent's deadline and floor. If the parent has no
// deadline, floor is applied directly. cancel must always be called.
func contextWithFloorTimeout(parent context.Context, floor time.Duration) (context.Context, context.CancelFunc) {
	if dl, ok := parent.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < floor {
			return context.WithDeadline(parent, dl)
		}
	}
	return context.WithTimeout(parent, floor)
}

// deadlineMillis is a forensic helper for the start-log line — how many
// ms of budget the call has at the start.
func deadlineMillis(ctx context.Context, now time.Time) int {
	dl, ok := ctx.Deadline()
	if !ok {
		return -1
	}
	return int(dl.Sub(now).Milliseconds())
}
