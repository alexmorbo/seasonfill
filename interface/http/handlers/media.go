package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/media"
	"github.com/alexmorbo/seasonfill/infrastructure/mediastore"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// mediaCacheMaxBytes is the LRU's byte cap — 32 MiB, matches the
// existing sonarr.LRUPosterCache budget. Sized for hot-path series
// detail browsing (a few hundred small images).
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
type MediaAssetReader interface {
	Get(ctx context.Context, hash string) (media.Asset, error)
	Upsert(ctx context.Context, a media.Asset) error
}

// mediaCacheEntry is the LRU's value type. Bytes is the decoded
// payload; ContentType is forwarded verbatim from upstream.
type mediaCacheEntry struct {
	Bytes       []byte
	ContentType string
}

// MediaHandler implements GET /api/v1/media/:hash. Three cache tiers:
// in-process LRU → mediastore → upstream refetch under singleflight.
//
// The handler is constructed once at boot and held for the life of
// the process; LRU + singleflight group are per-handler so a future
// per-tenant deploy keeps state isolated.
type MediaHandler struct {
	store  mediastore.Store
	repo   MediaAssetReader
	http   *http.Client
	cache  *byteCappedLRU
	sf     singleflight.Group
	logger *slog.Logger
	clock  func() time.Time
}

// NewMediaHandler wires the handler. http=nil → http.DefaultClient
// (acceptable for tests; production passes the TMDB-proxied client
// per §7).
func NewMediaHandler(store mediastore.Store, repo MediaAssetReader, httpClient *http.Client, logger *slog.Logger) *MediaHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &MediaHandler{
		store:  store,
		repo:   repo,
		http:   httpClient,
		cache:  newByteCappedLRU(mediaCacheMaxBytes),
		logger: logger,
		clock:  func() time.Time { return time.Now().UTC() },
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
// @Failure     404   {object}  dto.ErrorResponse  "pending / failed / unknown hash"
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

	// 2. Repo lookup. status=pending|failed → 404 (frontend
	//    placeholder). status=stored → mediastore Get; on lost-object
	//    fall through to upstream refetch under singleflight.
	asset, err := h.repo.Get(ctx, hash)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "media not found"})
			return
		}
		h.logger.WarnContext(ctx, "media.serve.repo_error",
			slog.String("hash", hash),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "media lookup failed"})
		return
	}
	if asset.Status != media.StatusStored {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "media not ready"})
		return
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

// byteCappedLRU is the LRU with byte-size eviction. Same shape as
// sonarr.LRUPosterCache (32 MiB cap, hashicorp/golang-lru v2
// store, sync.Mutex + size accountant).
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
