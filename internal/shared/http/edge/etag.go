package edge

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Section tokens for the ETag cache key + the synced_at column selector.
// Duplicated as bare string literals in the seriesdetail ETagFreshnessAdapter
// (it cannot import this package — edge -> seriesdetailrest -> seriesdetail
// would cycle). Keep the two lists in lockstep.
const (
	sectionSkeleton = "skeleton"
	sectionOverview = "overview"
	sectionCast     = "cast"
	sectionRecs     = "recs"
	sectionSeason   = "season"
)

const etagCacheControl = "private, max-age=60, stale-while-revalidate=600"

// SectionSyncedAtReader resolves the last-write timestamp for one canon
// series-detail section. Implemented structurally by
// seriesdetail.ETagFreshnessAdapter (no import back into edge). Returns
// (nil, nil) when the section was never synced (NULL stamp); the middleware
// fails open without an ETag. A non-nil error (including ports.ErrNotFound
// for an absent series/season) also fails open.
type SectionSyncedAtReader interface {
	SectionSyncedAt(ctx context.Context, seriesID domain.SeriesID, section string, seasonNumber int) (*time.Time, error)
}

// ETagMiddleware emits a weak ETag + Cache-Control on the enrichment-backed
// series-detail GET routes and short-circuits with 304 Not Modified when the
// client's If-None-Match matches. It is strictly additive: on any error, an
// unrecognised route, or a NULL/zero stamp it calls c.Next() unchanged — a
// caching optimisation must never 500 or alter a response body.
//
// gin runs route middleware before the handler, so a 304 abort here prevents
// the handler from ever executing (bandwidth + CPU saved).
func ETagMiddleware(reader SectionSyncedAtReader, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if reader == nil {
			c.Next()
			return
		}
		section, seasonNumber, ok := extractSection(c)
		if !ok {
			c.Next()
			return
		}
		parsedID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.Next() // malformed id — let the handler return its 400
			return
		}
		seriesID := domain.SeriesID(parsedID)
		lang := c.Query("lang")

		syncedAt, err := reader.SectionSyncedAt(c.Request.Context(), seriesID, section, seasonNumber)
		if err != nil {
			if logger != nil {
				logger.Debug("etag: synced_at lookup failed, skipping cache header",
					slog.Int("series_id", parsedID),
					slog.String("section", section),
					slog.String("error", err.Error()))
			}
			c.Next() // fail-open on lookup error
			return
		}
		if syncedAt == nil || syncedAt.IsZero() {
			c.Next() // never synced — nothing stable to key on
			return
		}

		tag := section
		if section == sectionSeason {
			tag = fmt.Sprintf("season:%d", seasonNumber)
		}
		key := fmt.Sprintf("%d-%d-%s-%s", int64(seriesID), syncedAt.Unix(), lang, tag)
		// Story 1087a — the cast endpoint takes an optional ?limit=N that
		// changes the response cardinality. Fold it into the ETag key so
		// ?limit=8 and the full page never share a 304. Only the cast section
		// carries a limit; absent/invalid/<=0 collapse to the un-suffixed
		// full-page key (must match the handler's parseCastLimit normalization).
		if section == sectionCast {
			if n, lerr := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); lerr == nil && n > 0 {
				key = fmt.Sprintf("%s-lim%d", key, n)
			}
			// Story 1087b — ?sort= reorders the cast body. Fold non-default
			// sorts into the key so an If-None-Match for one order never 304s a
			// differently-ordered body. "episodes" (default) + absent/unknown
			// collapse to the un-suffixed key (keeps the 1087a full-page ETag
			// shape) — must match the handler's parseCastSort normalization.
			switch strings.ToLower(strings.TrimSpace(c.Query("sort"))) {
			case "credit":
				key += "-srtcredit"
			case "name":
				key += "-srtname"
			}
		}
		serverETag := fmt.Sprintf(`W/"%s"`, key)

		if etagMatches(c.GetHeader("If-None-Match"), serverETag) {
			c.Status(http.StatusNotModified) // 304, no body
			c.Abort()
			return
		}

		c.Header("ETag", serverETag)
		c.Header("Cache-Control", etagCacheControl)
		// No Vary: Accept-Language — lang is a ?lang= query param, already
		// part of the URL cache key (plan L-1).
		c.Next()
	}
}

// extractSection maps the matched gin route pattern (c.FullPath()) to a
// section token. Season routes carry the season number in different param
// names (:n for /season/:n, :season for /seasons/:season/episodes — verified
// edge/server.go:255,257). Returns ok=false for any route the middleware
// should not touch, so callers fail open.
func extractSection(c *gin.Context) (section string, seasonNumber int, ok bool) {
	fp := c.FullPath()
	switch {
	case strings.HasSuffix(fp, "/overview"):
		return sectionOverview, 0, true
	case strings.HasSuffix(fp, "/cast"):
		return sectionCast, 0, true
	case strings.HasSuffix(fp, "/recommendations"):
		return sectionRecs, 0, true
	case strings.HasSuffix(fp, "/season/:n"):
		n, err := strconv.Atoi(c.Param("n"))
		if err != nil {
			return "", 0, false
		}
		return sectionSeason, n, true
	case strings.HasSuffix(fp, "/seasons/:season/episodes"):
		n, err := strconv.Atoi(c.Param("season"))
		if err != nil {
			return "", 0, false
		}
		return sectionSeason, n, true
	case strings.HasSuffix(fp, "/series/:id"):
		return sectionSkeleton, 0, true
	default:
		return "", 0, false
	}
}

// etagMatches reports whether the If-None-Match header matches serverETag.
// The header may be a comma-separated list; a "*" token matches any current
// representation (RFC 7232 §3.2). Whitespace around each token is trimmed.
// Weak comparison is exact-string here because both sides carry the W/ prefix.
func etagMatches(header, serverETag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	for tok := range strings.SplitSeq(header, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "*" || tok == serverETag {
			return true
		}
	}
	return false
}
