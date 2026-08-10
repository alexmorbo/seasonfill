// blocklist_handler.go ships the ADR-0017 Ф5 S3 discovery blocklist HTTP
// surface (global, pre-RBAC):
//
//	POST   /api/v1/discovery/blocklist        {kind, ref_id, label?} → 201 {id,kind,ref_id,label?}
//	GET    /api/v1/discovery/blocklist        → [ {id,kind,ref_id,title?,poster_hash?,label?} ]
//	DELETE /api/v1/discovery/blocklist/:id     → 204
//	GET    /api/v1/discovery/keyword-search?q= → [ {id,name} ]   (ADR-0017 Ф5 S3)
//
// Deliberately UNannotated for swagger (sibling /discovery/* convention —
// the FE hand-authors the DTOs). `make openapi` must stay no-diff.
package rest

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// BlocklistStore is the narrow persistence port the handler needs.
// Satisfied by *persistence.BlocklistRepository.
type BlocklistStore interface {
	Insert(ctx context.Context, kind disco.BlocklistKind, refID int64, label *string) (disco.BlocklistEntry, error)
	DeleteByID(ctx context.Context, id int64) error
	ListResolved(ctx context.Context, language string) ([]persistence.ResolvedBlocklistRow, error)
}

// KeywordSearcher proxies TMDB /search/keyword. Satisfied by a wiring
// adapter over *tmdb.Client. Nil-OK: when nil the keyword-search route
// returns 503 (TMDB disabled / unwired). ADR-0017 Ф5 S3.
type KeywordSearcher interface {
	SearchKeyword(ctx context.Context, query string) ([]KeywordHit, error)
}

// KeywordHit is the wire shape for one /keyword-search result. Emitted as a
// bare JSON array element (FE contract).
type KeywordHit struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// BlocklistHandler serves the discovery blocklist endpoints.
type BlocklistHandler struct {
	store    BlocklistStore
	cache    *app.BlocklistCache
	keywords KeywordSearcher // nil-OK → keyword-search returns 503
	resolver *media.Resolver // nil-OK → poster_hash falls back to raw asset path
	log      *slog.Logger
}

// NewBlocklistHandler wires the handler. store/cache/log required;
// keywords + resolver are nil-OK.
func NewBlocklistHandler(
	store BlocklistStore,
	cache *app.BlocklistCache,
	keywords KeywordSearcher,
	resolver *media.Resolver,
	log *slog.Logger,
) *BlocklistHandler {
	switch {
	case store == nil:
		panic("blocklist handler: store required")
	case cache == nil:
		panic("blocklist handler: cache required")
	case log == nil:
		panic("blocklist handler: log required")
	}
	return &BlocklistHandler{store: store, cache: cache, keywords: keywords, resolver: resolver, log: log}
}

// blocklistCreateRequest is the POST body.
type blocklistCreateRequest struct {
	Kind  string  `json:"kind"`
	RefID int64   `json:"ref_id"`
	Label *string `json:"label,omitempty"`
}

// blocklistCreateResponse is the POST 201 body — the persisted row.
type blocklistCreateResponse struct {
	ID    int64   `json:"id"`
	Kind  string  `json:"kind"`
	RefID int64   `json:"ref_id"`
	Label *string `json:"label,omitempty"`
}

// BlocklistItem is one GET /discovery/blocklist row on the wire.
type BlocklistItem struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	RefID      int64   `json:"ref_id"`
	Title      *string `json:"title,omitempty"`
	PosterHash *string `json:"poster_hash,omitempty"`
	Label      *string `json:"label,omitempty"`
}

// Create serves POST /discovery/blocklist. Idempotent: a duplicate
// (kind, ref_id) returns 201 with the pre-existing row (already blocked is
// success, never 500).
func (h *BlocklistHandler) Create(c *gin.Context) {
	var req blocklistCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_body", "malformed blocklist body")
		return
	}
	kind := disco.BlocklistKind(strings.TrimSpace(req.Kind))
	if !kind.IsValid() {
		respondError(c, http.StatusBadRequest, "invalid_kind", "kind must be 'tmdb' or 'keyword'")
		return
	}
	if req.RefID <= 0 {
		respondError(c, http.StatusBadRequest, "invalid_ref_id", "ref_id must be a positive integer")
		return
	}
	label := req.Label
	if label != nil {
		trimmed := strings.TrimSpace(*label)
		if trimmed == "" {
			label = nil
		} else {
			label = &trimmed
		}
	}
	ctx := c.Request.Context()
	entry, err := h.store.Insert(ctx, kind, req.RefID, label)
	if err != nil {
		h.log.WarnContext(ctx, "discovery.blocklist.insert_failed",
			slog.String("kind", string(kind)),
			slog.Int64("ref_id", req.RefID),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "blocklist_write_failed", "could not persist blocklist entry")
		return
	}
	// Refresh the in-memory sets so the next discovery read subtracts this
	// entry immediately (bumps epoch → invalidates discover LRU pages).
	if err := h.cache.Refresh(ctx); err != nil {
		h.log.WarnContext(ctx, "discovery.blocklist.cache_refresh_failed",
			slog.String("error", err.Error()))
		// Non-fatal: the row is persisted; a later mutation or boot reload
		// picks it up. Still return 201 — the write succeeded.
	}
	c.JSON(http.StatusCreated, blocklistCreateResponse{
		ID:    entry.ID,
		Kind:  string(entry.Kind),
		RefID: entry.RefID,
		Label: entry.Label,
	})
}

// List serves GET /discovery/blocklist. Returns a bare JSON array (FE
// contract). tmdb rows carry the resolved title + poster_hash; keyword
// rows carry the label.
func (h *BlocklistHandler) List(c *gin.Context) {
	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_language", "lang must be a BCP-47 tag")
		return
	}
	ctx := c.Request.Context()
	rows, err := h.store.ListResolved(ctx, lang)
	if err != nil {
		h.log.WarnContext(ctx, "discovery.blocklist.list_failed",
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "blocklist_read_failed", "could not read blocklist")
		return
	}
	items := make([]BlocklistItem, 0, len(rows))
	for _, r := range rows {
		it := BlocklistItem{ID: r.ID, Kind: r.Kind, RefID: r.RefID, Label: r.Label}
		if r.Kind == string(disco.BlocklistKindTMDB) {
			it.Title = r.Title
			it.PosterHash = h.resolvePoster(ctx, r.PosterAsset)
		}
		items = append(items, it)
	}
	c.JSON(http.StatusOK, items)
}

// resolvePoster maps a raw series_media_texts.poster_asset path to the
// sha256 wire hash the FE serves via /api/v1/media/:hash (same "w342" /
// "poster_w342" slot the discovery tiles use). Nil resolver or nil path →
// returns the raw path unchanged (nil stays nil).
func (h *BlocklistHandler) resolvePoster(ctx context.Context, asset *string) *string {
	if asset == nil || h.resolver == nil {
		return asset
	}
	if hash := h.resolver.Resolve(ctx, asset, "w342", "poster_w342"); hash != nil {
		return hash
	}
	return asset
}

// Delete serves DELETE /discovery/blocklist/:id. Idempotent → 204.
func (h *BlocklistHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(c, http.StatusBadRequest, "invalid_id", "id must be a positive integer")
		return
	}
	ctx := c.Request.Context()
	if err := h.store.DeleteByID(ctx, id); err != nil {
		h.log.WarnContext(ctx, "discovery.blocklist.delete_failed",
			slog.Int64("id", id),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "blocklist_write_failed", "could not delete blocklist entry")
		return
	}
	if err := h.cache.Refresh(ctx); err != nil {
		h.log.WarnContext(ctx, "discovery.blocklist.cache_refresh_failed",
			slog.String("error", err.Error()))
	}
	c.Status(http.StatusNoContent)
}

// KeywordSearch serves GET /discovery/keyword-search?q=…. Proxies TMDB
// /search/keyword and returns a BARE JSON array [{id,name}] (FE contract) —
// never null. 503 when the searcher is unwired (TMDB disabled), 400 on a
// blank / over-long q, 502 on an upstream failure.
func (h *BlocklistHandler) KeywordSearch(c *gin.Context) {
	if h.keywords == nil {
		respondError(c, http.StatusServiceUnavailable, "search_unavailable", "keyword search not wired (TMDB disabled)")
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" || len(q) > 100 {
		respondError(c, http.StatusBadRequest, "invalid_query", "q must be 1..100 characters after trim")
		return
	}
	ctx := c.Request.Context()
	hits, err := h.keywords.SearchKeyword(ctx, q)
	if err != nil {
		h.log.WarnContext(ctx, "discovery.keyword_search.failed",
			slog.String("query", q),
			slog.String("error", err.Error()))
		respondError(c, http.StatusBadGateway, "tmdb_unavailable", "keyword search failed")
		return
	}
	if hits == nil {
		hits = []KeywordHit{}
	}
	c.JSON(http.StatusOK, hits)
}
