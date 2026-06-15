package handlers

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"

	appmedia "github.com/alexmorbo/seasonfill/application/media"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// mediaCacheMaxBytes is the LRU's byte cap — 32 MiB. Sized for hot-path
// series detail browsing (a few hundred small images).
const mediaCacheMaxBytes int64 = 32 << 20

// mediaCacheMaxEntries is the defensive entry cap; byte-size eviction
// kicks in long before this in normal operation.
const mediaCacheMaxEntries = 65536

// mediaCacheEntryOverhead approximates per-entry bookkeeping in the
// LRU (key string, value header, map slot). Added once per entry so
// eviction errs early.
const mediaCacheEntryOverhead = 64

// mediaCacheControl is the verbatim Cache-Control header value the
// handler writes on every 200/304 reply. One-year immutable per PRD
// §6.5 (content-addressed = stable bytes).
const mediaCacheControl = "public, max-age=31536000, immutable"

// mediaRefetchMaxBytes is the per-refetch body cap on the lost-object
// recovery path. Mirrors the downloader's maxBodyBytes.
const mediaRefetchMaxBytes int64 = 32 << 20

// MediaAssetReader is the read-only repo port the handler consumes.
// Production impl is *repositories.MediaAssetsRepository.
//
// Upsert is retained on the port so the on-demand fetcher (which the
// handler holds a reference to but does not invoke directly for
// status writes) can stamp status='stored' on a successful fetch.
// The handler itself never persists status='failed' — a failed fetch
// leaves the row's status untouched so the next request retries.
type MediaAssetReader interface {
	Get(ctx context.Context, hash string) (media.Asset, error)
	Upsert(ctx context.Context, a media.Asset) error
}

// MediaOnDemandSyncFetcher is the handler's hook into the on-demand
// fetcher (application/media.OnDemandFetcher). Story 321: when the
// media_assets row is status='pending', the handler reaches via this
// hook to synchronously pull the bytes from TMDB before serving.
// Returns (hash, true) on success — bytes are in mediastore + the row
// has been upserted to status='stored'. ("", false) on timeout / fail.
// Nil-OK: the handler keeps the legacy 404-on-pending behavior when
// the fetcher isn't wired (boot ordering or media subsystem disabled).
type MediaOnDemandSyncFetcher interface {
	FetchSync(ctx context.Context, upstreamURL, kind, ext string) (string, bool)
}

// MediaPendingResolver is the handler's lookup from hash to the row's
// source_url + kind + status. Story 321: used on the status='pending'
// path to recover the URL the fetcher needs. Story 320 ships the
// repo-side GetSourceURLByHash method on *MediaAssetsRepository.
type MediaPendingResolver interface {
	GetSourceURLByHash(ctx context.Context, hash string) (sourceURL, kind string, status media.Status, err error)
}

// mediaCacheEntry is the LRU's value type. Bytes is the decoded
// payload; ContentType is forwarded verbatim from upstream.
type mediaCacheEntry struct {
	Bytes       []byte
	ContentType string
}

// mediaPlaceholderSVG is the static "no image" SVG served when the
// on-demand fetcher misses (TMDB error, timeout, banned URL). Embedded
// at build time so the binary stays self-contained. Story 321 override
// from operator: serve a placeholder instead of 404 so the frontend
// always renders a stable visual instead of a broken-image icon.
//
//go:embed assets/media_placeholder.svg
var mediaPlaceholderSVG []byte

// mediaPlaceholderContentType is the verbatim Content-Type for the
// embedded SVG placeholder.
const mediaPlaceholderContentType = "image/svg+xml"

// mediaPlaceholderCacheControl caps placeholder caching at 5 minutes
// so the frontend reattempts within a reasonable window once the row
// transitions to status='stored'.
const mediaPlaceholderCacheControl = "public, max-age=300"

// MediaHandler implements GET /api/v1/media/:hash. Cache tiers:
// in-process LRU → mediastore → on-demand sync fetch (story 321,
// for status='pending' rows) → upstream refetch under singleflight
// (lost-object recovery for status='stored' rows).
//
// The handler is constructed once at boot and held for the life of
// the process; LRU + singleflight group are per-handler so a future
// per-tenant deploy keeps state isolated.
type MediaHandler struct {
	store              mediastore.Store
	repo               MediaAssetReader
	pendingResolver    MediaPendingResolver     // story 321 — may be nil
	ondemandFetcher    MediaOnDemandSyncFetcher // story 321 — may be nil
	negativeCacheTTL   time.Duration            // story 321 — failed-at re-attempt window
	ondemandWallBudget time.Duration            // story 321 — per-request budget for FetchSync
	http               *http.Client
	cache              *byteCappedLRU
	sf                 singleflight.Group
	logger             *slog.Logger
	clock              func() time.Time
}

// MediaHandlerDeps groups the handler's wiring. Kept as a struct so the
// caller specifies named fields — the constructor's positional shape was
// brittle once the on-demand fetcher landed.
type MediaHandlerDeps struct {
	Store              mediastore.Store
	Repo               MediaAssetReader
	PendingResolver    MediaPendingResolver     // story 321: may be nil
	OnDemandFetcher    MediaOnDemandSyncFetcher // story 321: may be nil
	HTTPClient         *http.Client
	Logger             *slog.Logger
	NegativeCacheTTL   time.Duration // story 321: 0 → defaultNegativeCacheTTL (60 s)
	OnDemandWallBudget time.Duration // story 321: 0 → defaultOnDemandWallBudget (2 s)
}

// defaultNegativeCacheTTL is the window after a failed on-demand fetch
// during which the handler short-circuits to the placeholder without
// re-attempting. 60 s balances "operator hits refresh hoping for a fix"
// against "don't hammer TMDB for a permanently dead path." Override via
// env in main.go.
const defaultNegativeCacheTTL = 60 * time.Second

// defaultOnDemandWallBudget is the per-request budget passed to
// FetchSync. TMDB-via-VPN p50 ~600 ms; 2 s leaves 2× headroom and stays
// well under browser image-fetch tolerance.
const defaultOnDemandWallBudget = 2 * time.Second

// SetOnDemandFetcher late-binds the on-demand fetcher onto an
// already-constructed handler. Used by cmd/server/main.go: the handler
// is created before wireEnrichment runs (so router registration can
// take a stable *MediaHandler pointer), then the fetcher is plugged in
// once the media pipeline is up. Concurrent reads are safe — the
// handler's Serve only reads the field; the late-bind happens once
// during boot before the HTTP server starts serving.
func (h *MediaHandler) SetOnDemandFetcher(f MediaOnDemandSyncFetcher) {
	if h == nil {
		return
	}
	h.ondemandFetcher = f
}

// SetPendingResolver late-binds the pending resolver onto an
// already-constructed handler. Symmetric to SetOnDemandFetcher — used
// when the resolver isn't available at NewMediaHandler time.
func (h *MediaHandler) SetPendingResolver(r MediaPendingResolver) {
	if h == nil {
		return
	}
	h.pendingResolver = r
}

// NewMediaHandler wires the handler.
//
// Story 321: signature changed from (store, repo, httpClient, logger)
// to a deps struct. PendingResolver + OnDemandFetcher MAY be nil — the
// handler falls back to the legacy "404 on non-stored" behavior in
// that case (used during boot ordering, or when the media subsystem
// is disabled).
func NewMediaHandler(d MediaHandlerDeps) *MediaHandler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	negTTL := d.NegativeCacheTTL
	if negTTL <= 0 {
		negTTL = defaultNegativeCacheTTL
	}
	wall := d.OnDemandWallBudget
	if wall <= 0 {
		wall = defaultOnDemandWallBudget
	}
	return &MediaHandler{
		store:              d.Store,
		repo:               d.Repo,
		pendingResolver:    d.PendingResolver,
		ondemandFetcher:    d.OnDemandFetcher,
		negativeCacheTTL:   negTTL,
		ondemandWallBudget: wall,
		http:               httpClient,
		cache:              newByteCappedLRU(mediaCacheMaxBytes),
		logger:             logger,
		clock:              func() time.Time { return time.Now().UTC() },
	}
}

// Serve handles GET /api/v1/media/:hash.
//
// @Summary     Stream a stored media asset
// @Description Streams the content-addressed asset bytes. Cache-Control
// @Description is one year immutable (hash-based URLs are stable);
// @Description ETag is the hash itself so If-None-Match always 304s.
// @Tags        media
// @Produce     image/jpeg
// @Param       hash  path      string  true  "sha256 hex of the upstream URL"
// @Success     200   {string}  binary  "asset bytes"
// @Success     304   "not modified"
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse  "reserved (handler currently has no 404 paths)"
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /media/{hash} [get]
func (h *MediaHandler) Serve(c *gin.Context) {
	hash := c.Param("hash")
	if !isValidHashHex(hash) {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid hash"})
		return
	}
	ctx := c.Request.Context()

	// Story 347 — sentinel served as the embedded SVG placeholder
	// without a DB roundtrip. Composer hands the FE this hash when an
	// asset has no raw path / no recoverable source URL; the handler
	// short-circuits before any repo / store call. Intentionally
	// always 200 (no 304) — sentinel responses are tiny and a
	// freshly-deployed FE doesn't carry a stale ETag past a
	// resolver flip.
	if hash == appmedia.SentinelMissingHash {
		h.writeSentinel(c)
		return
	}

	ifNoneMatch := c.GetHeader("If-None-Match")
	etag := `"` + hash + `"`

	// 1. LRU hit — serve from memory.
	if entry, ok := h.cache.Get(hash); ok {
		if matchesETag(ifNoneMatch, etag) {
			h.write304(c, etag)
			return
		}
		h.write200(c, entry, etag)
		h.logger.DebugContext(ctx, "media.serve.lru_hit",
			slog.String("hash", hash),
			slog.Int("size", len(entry.Bytes)),
		)
		return
	}

	// 2. Repo lookup. status=stored → mediastore Get; any other status
	//    (pending OR failed) → on-demand sync fetch; on miss we serve
	//    the SVG placeholder for THIS request but do not persist any
	//    negative-cache state, so the next request tries again. Operator
	//    decision: a transient failure must heal itself on reload rather
	//    than wait for the downloader's background retry loop.
	asset, err := h.repo.Get(ctx, hash)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			// Story 352: catalog endpoints (ListSeriesCache,
			// enrichMissingFromCache, collectGrabCacheFields) emit
			// deterministic eager poster_hash values into the wire DTO
			// before media_assets has a pending row — the EnsurePending
			// side effect runs in a background goroutine after the
			// response commits. Serving 404 here breaks <img onError>
			// into the monogram fallback for the milliseconds between
			// the FE receiving the hash and the goroutine landing the
			// row. The SVG placeholder (200 + image/svg+xml + 5-min
			// Cache-Control) keeps the visual stable; the FE re-requests
			// once the cache window expires and by then the row is
			// pending → on-demand fetch path takes over.
			h.writePlaceholder(c, hash, "unknown_hash")
			return
		}
		h.logger.WarnContext(ctx, "media.serve.repo_error",
			slog.String("hash", hash),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "media lookup failed"})
		return
	}
	if asset.Status != media.StatusStored {
		// Story 321: try synchronous on-demand fetch for pending rows
		// (failed rows short-circuit straight to placeholder). Falls back
		// to placeholder on legacy wiring (fetcher / resolver not
		// plumbed), budget exhaustion, or unrecoverable upstream errors.
		// Re-reads the asset on hit so the rest of the handler (LRU,
		// refetch loop) operates on the freshly-stored row.
		fresh, ok := h.serveOnDemand(c, ctx, hash, asset)
		if !ok {
			return
		}
		asset = fresh
	}

	// 3. mediastore Get. Lost object → refetch under singleflight.
	entry, served, err := h.loadFromStoreOrRefetch(ctx, asset)
	if err != nil {
		h.logger.WarnContext(ctx, "media.serve.load_failed",
			slog.String("hash", hash),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "media load failed"})
		return
	}

	h.cache.Put(hash, entry)

	if matchesETag(ifNoneMatch, etag) {
		h.write304(c, etag)
		return
	}
	h.write200(c, entry, etag)

	h.logger.DebugContext(ctx, "media.serve.ok",
		slog.String("hash", hash),
		slog.String("source", served),
		slog.Int("size", len(entry.Bytes)),
	)
}

// loadFromStoreOrRefetch returns the entry + a descriptive source
// label ("store" / "upstream"). Singleflight is keyed on the hash so
// two concurrent first-requests dedup to one upstream fetch.
func (h *MediaHandler) loadFromStoreOrRefetch(ctx context.Context, asset media.Asset) (mediaCacheEntry, string, error) {
	rc, info, err := h.store.Get(ctx, mediastore.Key(asset.UpstreamURL, extFromContentType(asset.ContentType)))
	if err == nil {
		defer func() { _ = rc.Close() }()
		body, rerr := io.ReadAll(rc)
		if rerr != nil {
			return mediaCacheEntry{}, "", rerr
		}
		ct := info.ContentType
		if ct == "" {
			ct = asset.ContentType
		}
		return mediaCacheEntry{Bytes: body, ContentType: ct}, "store", nil
	}
	if !errors.Is(err, mediastore.ErrNotFound) && !errors.Is(err, mediastore.ErrNotSupported) {
		return mediaCacheEntry{}, "", err
	}

	// Lost object — singleflight refetch from upstream.
	sfRes, sfErr, _ := h.sf.Do(asset.Hash, func() (interface{}, error) {
		return h.refetchAndStore(ctx, asset)
	})
	if sfErr != nil {
		return mediaCacheEntry{}, "", sfErr
	}
	entry, ok := sfRes.(mediaCacheEntry)
	if !ok {
		return mediaCacheEntry{}, "", errors.New("singleflight result type mismatch")
	}
	h.logger.WarnContext(ctx, "media.serve.lost_object_recovered",
		slog.String("hash", asset.Hash),
		slog.String("upstream_url", asset.UpstreamURL),
		slog.Int("size", len(entry.Bytes)),
	)
	return entry, "upstream", nil
}

// refetchAndStore is the upstream-fetch leg of the singleflight
// closure. Fetches the upstream URL, Puts bytes back to the store,
// returns the entry. Does NOT update the media_assets row — the row
// is already status=stored with the right content-type / size from
// the original Put.
func (h *MediaHandler) refetchAndStore(ctx context.Context, asset media.Asset) (mediaCacheEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.UpstreamURL, nil)
	if err != nil {
		return mediaCacheEntry{}, err
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return mediaCacheEntry{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return mediaCacheEntry{}, errors.New("upstream refetch status " + http.StatusText(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, mediaRefetchMaxBytes))
	if err != nil {
		return mediaCacheEntry{}, err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = asset.ContentType
	}
	key := mediastore.Key(asset.UpstreamURL, extFromContentType(ct))
	if perr := h.store.Put(ctx, key, bytes.NewReader(body), int64(len(body)), ct); perr != nil {
		h.logger.WarnContext(ctx, "media.serve.refetch_put_failed",
			slog.String("hash", asset.Hash),
			slog.String("error", perr.Error()))
		// Still serve — refetch succeeded; subsequent requests will
		// retry the Put on the next lost-object hit.
	}
	return mediaCacheEntry{Bytes: body, ContentType: ct}, nil
}

// serveOnDemand is the story 321 sync-fetch path. Called when
// repo.Get returns a non-stored asset (pending or failed). Returns
// (freshAsset, true) when the fetch succeeded + the caller should
// continue serving from store; otherwise writes the placeholder (200 +
// image/svg+xml + X-Media-Placeholder: 1) and returns (_, false).
//
// Every request tries the upstream fetch — there is no negative cache
// keyed on status='failed'. The downloader's own retry loop owns the
// status transitions; this handler is a pure read-path that prefers
// "serve placeholder for this one request and try again next time"
// over latching the row into a sticky failure state. Singleflight
// keyed on the hash collapses concurrent tabs onto a single upstream
// call.
func (h *MediaHandler) serveOnDemand(c *gin.Context, ctx context.Context, hash string, asset media.Asset) (media.Asset, bool) {
	// Bypass when the wiring isn't there yet — serve the placeholder so
	// the frontend stays visually stable.
	if h.ondemandFetcher == nil || h.pendingResolver == nil {
		h.writePlaceholder(c, hash, "wiring_unavailable")
		return media.Asset{}, false
	}

	// status=pending OR status=failed (or any non-stored) → sync fetch.
	// Failed rows are NOT short-circuited: the operator decision is to
	// retry on every request so a transient TMDB / VPN hiccup heals
	// itself the next time the user reloads, without waiting for the
	// downloader's background sweep.
	sourceURL, kind, _, lerr := h.pendingResolver.GetSourceURLByHash(ctx, hash)
	if lerr != nil || sourceURL == "" {
		h.logger.WarnContext(ctx, "media.serve.pending_no_source",
			slog.String("hash", hash),
			slog.String("error", errString(lerr)),
		)
		h.writePlaceholder(c, hash, "no_source_url")
		return media.Asset{}, false
	}

	// Per-request budget — capped at h.ondemandWallBudget.
	fetchCtx, cancel := context.WithTimeout(ctx, h.ondemandWallBudget)
	defer cancel()

	// Singleflight per hash. The closure returns sfResult; concurrent
	// callers see the same negative result without retrying.
	type sfResult struct {
		ok    bool
		asset media.Asset
	}
	resAny, _, _ := h.sf.Do("ondemand:"+hash, func() (interface{}, error) {
		_, ok := h.ondemandFetcher.FetchSync(fetchCtx, sourceURL, kind, extFromSourceURL(sourceURL))
		if !ok {
			// No negative-cache persist: leave the row's current status
			// alone so the next request gets another fetch attempt. The
			// downloader's background retry loop owns the status flip
			// to 'failed' when its own attempts give up.
			h.logger.WarnContext(ctx, "media.serve.ondemand_miss",
				slog.String("hash", hash),
				slog.String("kind", kind),
				slog.String("source_url", sourceURL),
			)
			return sfResult{ok: false}, nil
		}
		// Re-read the row to get the freshly-stamped status/content_type/size.
		updated, gerr := h.repo.Get(ctx, hash)
		if gerr != nil {
			h.logger.WarnContext(ctx, "media.serve.ondemand_reread_failed",
				slog.String("hash", hash),
				slog.String("error", gerr.Error()),
			)
			return sfResult{ok: false}, nil
		}
		h.logger.InfoContext(ctx, "media.serve.ondemand_filled",
			slog.String("hash", hash),
			slog.String("kind", kind),
			slog.Int64("size_bytes", updated.Size),
		)
		return sfResult{ok: true, asset: updated}, nil
	})
	res, _ := resAny.(sfResult)
	if !res.ok {
		h.writePlaceholder(c, hash, "fetch_failed")
		return media.Asset{}, false
	}
	return res.asset, true
}

// writePlaceholder serves the embedded SVG with a 5-minute cache window
// and an X-Media-Placeholder debug header. Reason is logged so we can
// trace WHY the placeholder fired from logs / NetworkTab.
func (h *MediaHandler) writePlaceholder(c *gin.Context, hash, reason string) {
	c.Header("Cache-Control", mediaPlaceholderCacheControl)
	c.Header("X-Media-Placeholder", "1")
	c.Data(http.StatusOK, mediaPlaceholderContentType, mediaPlaceholderSVG)
	h.logger.DebugContext(c.Request.Context(), "media.serve.placeholder",
		slog.String("hash", hash),
		slog.String("reason", reason),
	)
}

// writeSentinel serves the embedded SVG placeholder for the story-347
// sentinel hash. Distinct from writePlaceholder so logs / NetworkTab
// can tell the deterministic "no canonical asset" case apart from the
// on-demand-fetch failure path. Cache window is 24h — sentinel hashes
// never transition, so browsers can hold the response indefinitely
// (the seed is namespaced "...:v1" so a future rotation is the escape
// hatch).
func (h *MediaHandler) writeSentinel(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=86400")
	c.Header("X-Media-Placeholder", "sentinel")
	c.Data(http.StatusOK, mediaPlaceholderContentType, mediaPlaceholderSVG)
	h.logger.DebugContext(c.Request.Context(), "media.serve.sentinel")
}

// extFromSourceURL extracts the lowercase extension from a URL path.
// Mirrors application/media.ExtractExt for the handler's needs (the
// extension is just a hint for the store key).
func extFromSourceURL(url string) string {
	// Trim query string if any.
	if i := indexByteFromEnd(url, '?'); i >= 0 {
		url = url[:i]
	}
	dot := indexByteFromEnd(url, '.')
	slash := indexByteFromEnd(url, '/')
	if dot < 0 || dot < slash || dot == len(url)-1 {
		return ""
	}
	ext := strings.ToLower(url[dot+1:])
	switch ext {
	case "jpg", "jpeg", "png", "webp", "gif":
		return ext
	}
	return ""
}

func indexByteFromEnd(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func (h *MediaHandler) write200(c *gin.Context, entry mediaCacheEntry, etag string) {
	ct := entry.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("ETag", etag)
	c.Header("Cache-Control", mediaCacheControl)
	c.Data(http.StatusOK, ct, entry.Bytes)
}

func (h *MediaHandler) write304(c *gin.Context, etag string) {
	c.Header("ETag", etag)
	c.Header("Cache-Control", mediaCacheControl)
	c.Status(http.StatusNotModified)
}

// isValidHashHex returns true iff s is 64 lowercase hex chars.
// Defensive — guards against path-injection attempts on :hash.
func isValidHashHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// matchesETag checks the If-None-Match header against our minted
// etag value. RFC 7232 allows either quoted or weak ("W/" prefix);
// we accept both since the etag is hash-based + stable.
func matchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	if ifNoneMatch == etag {
		return true
	}
	// Strip a "W/" prefix if present.
	if len(ifNoneMatch) > 2 && ifNoneMatch[:2] == "W/" {
		return ifNoneMatch[2:] == etag
	}
	return false
}

// extFromContentType maps an HTTP content-type to the mediastore key
// extension. Empty content-type yields empty extension (the
// mediastore key still resolves; the extension is purely a hint).
func extFromContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return ""
}

// byteCappedLRU is the LRU with byte-size eviction (32 MiB cap,
// hashicorp/golang-lru v2 store, sync.Mutex + size accountant).
type byteCappedLRU struct {
	mu        sync.Mutex
	store     *lru.Cache[string, mediaCacheEntry]
	maxBytes  int64
	totalSize int64
}

func newByteCappedLRU(maxBytes int64) *byteCappedLRU {
	if maxBytes <= 0 {
		maxBytes = mediaCacheMaxBytes
	}
	store, _ := lru.New[string, mediaCacheEntry](mediaCacheMaxEntries)
	return &byteCappedLRU{store: store, maxBytes: maxBytes}
}

func (c *byteCappedLRU) Get(hash string) (mediaCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Get(hash)
}

func (c *byteCappedLRU) Put(hash string, entry mediaCacheEntry) {
	size := entrySize(hash, entry)
	if size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.store.Peek(hash); ok {
		c.totalSize -= entrySize(hash, old)
	}
	c.store.Add(hash, entry)
	c.totalSize += size
	for c.totalSize > c.maxBytes {
		oldestKey, oldestEntry, ok := c.store.GetOldest()
		if !ok {
			break
		}
		c.store.Remove(oldestKey)
		c.totalSize -= entrySize(oldestKey, oldestEntry)
	}
	if c.totalSize < 0 {
		c.totalSize = 0
	}
}

func (c *byteCappedLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Len()
}

func entrySize(hash string, e mediaCacheEntry) int64 {
	return int64(len(e.Bytes)) + int64(len(hash)) + mediaCacheEntryOverhead
}
