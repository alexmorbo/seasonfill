// Package media — Story 316 on-demand fetcher.
//
// FetchSync is the synchronous variant of Downloader.handle: it
// fetches a single TMDB-style asset, puts the bytes to the store,
// upserts the media_assets row, returns the sha256 hash. Designed
// for the page-composer "first-fold" path (hero poster/backdrop, person
// portrait) so the visited assets land in S3 before the response goes
// out. Cap'd by the shared 5 rps rate limiter so a hot page can't
// hammer TMDB.
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
	"time"

	"golang.org/x/time/rate"

	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
)

// onDemandTimeout caps a single FetchSync invocation. Callers may pass
// a tighter ctx deadline; the http client also enforces this as a hard
// floor.
const onDemandTimeout = 1500 * time.Millisecond

// OnDemandFetcher is the public interface; *onDemandFetcher is the
// production impl. Kept as an interface so the composer test stubs
// can verify the call shape without touching the downloader's wire.
type OnDemandFetcher interface {
	FetchSync(ctx context.Context, upstreamURL, kind, ext string) (string, bool)
}

// OnDemandDeps groups the prod wiring. Mirrors DownloaderDeps so the
// wiring code can hand the same Store / Repo / HTTPClient. Limiter
// MUST be the shared *rate.Limiter the Downloader uses (otherwise the
// 5 rps cap gets split).
type OnDemandDeps struct {
	Store      mediastore.Store
	Repo       AssetRepo
	HTTPClient *http.Client
	Limiter    *rate.Limiter
	Logger     *slog.Logger
	Clock      func() time.Time
}

// onDemandFetcher is the production OnDemandFetcher.
type onDemandFetcher struct {
	store   mediastore.Store
	repo    AssetRepo
	http    *http.Client
	limiter *rate.Limiter
	logger  *slog.Logger
	clock   func() time.Time
}

// NewOnDemandFetcher constructs the prod fetcher. The shared Limiter
// (the same *rate.Limiter the Downloader holds) is required to keep
// the global 5 rps cap intact across sync + async paths.
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
	if d.HTTPClient == nil {
		d.HTTPClient = &http.Client{Timeout: onDemandTimeout}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &onDemandFetcher{
		store:   d.Store,
		repo:    d.Repo,
		http:    d.HTTPClient,
		limiter: d.Limiter,
		logger:  d.Logger,
		clock:   d.Clock,
	}, nil
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

	// Inherit caller deadline; if none, apply onDemandTimeout as a hard
	// floor so the composer can't accidentally hold the response.
	ctx, cancel := contextWithFloorTimeout(ctx, onDemandTimeout)
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
	if info, err := f.store.Stat(ctx, key); err == nil {
		_ = f.upsert(ctx, media.Asset{
			Hash:        hash,
			UpstreamURL: clean,
			Kind:        kind,
			ContentType: info.ContentType,
			Size:        info.Size,
			Status:      media.StatusStored,
		}, log)
		log.DebugContext(ctx, "media.ondemand.stat_hit",
			slog.Int("duration_ms", int(f.clock().Sub(start).Milliseconds())))
		return hash, true
	} else if !errors.Is(err, mediastore.ErrNotFound) && !errors.Is(err, mediastore.ErrNotSupported) {
		log.WarnContext(ctx, "media.ondemand.stat_error",
			slog.String("error", err.Error()))
		// Fall through — upstream fetch is the source of truth.
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
		}
		return "", false
	}

	// 3. HTTP GET.
	body, contentType, err := f.fetchOnce(ctx, clean)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			log.WarnContext(ctx, "media.ondemand.timeout",
				slog.String("stage", "http"))
			return "", false
		}
		log.WarnContext(ctx, "media.ondemand.failed",
			slog.String("error_kind", string(ClassifyFetchError(err))),
			slog.Int("http_status", HTTPStatus(err)),
			slog.String("error", err.Error()))
		return "", false
	}

	// 4. Put bytes to store.
	if err := f.store.Put(ctx, key, bytes.NewReader(body), int64(len(body)), contentType); err != nil {
		log.WarnContext(ctx, "media.ondemand.failed",
			slog.String("error_kind", string(ErrorKindS3Write)),
			slog.String("error", err.Error()))
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
		return "", false
	}

	log.InfoContext(ctx, "media.ondemand.ok",
		slog.Int("size_bytes", len(body)),
		slog.String("content_type", contentType),
		slog.Int("duration_ms", int(f.clock().Sub(start).Milliseconds())),
	)
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
