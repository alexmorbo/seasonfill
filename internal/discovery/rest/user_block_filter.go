// user_block_filter.go is the read-time per-user discovery blocklist chokepoint
// (Ф8-U-5b). The shared discover LRU stays GLOBAL/anonymous/RAW-keyed; the
// per-user tmdb + keyword subtraction happens only on the response copy handed
// to the current client, resolved once per request from gin.Context. Background
// / warming / no-user paths filter NOTHING (never another user's set) — this is
// the Ф5-S3 B1 warming-leak invariant restated per-user.
package rest

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// ResultKeywords batch-loads the TMDB keyword-ids for a page of results, keyed
// by series tmdb_id. Narrow port over the DB (kept off the handler so the rest
// layer never holds a raw *gorm.DB). Wiring supplies resultKeywordsAdapter.
type ResultKeywords interface {
	ResultKeywords(ctx context.Context, tmdbIDs []int64) (map[int64][]int64, error)
}

// userBlockFilter is the read-time per-user blocklist chokepoint shared by
// DiscoverHandler (passthrough) and DiscoveryHandler (curated). A nil
// *userBlockFilter is a valid no-op (filters nothing) so minimal wirings and
// legacy tests compile unchanged.
type userBlockFilter struct {
	users    dataports.UserRepository
	loader   app.BlocklistLoader // *persistence.BlocklistRepository (LoadBlockSets)
	keywords ResultKeywords
	log      *slog.Logger
}

func newUserBlockFilter(users dataports.UserRepository, loader app.BlocklistLoader, keywords ResultKeywords, log *slog.Logger) *userBlockFilter {
	return &userBlockFilter{users: users, loader: loader, keywords: keywords, log: log}
}

// currentUserBlocks resolves the authenticated user ONCE and loads BOTH their
// tmdb and keyword block sets for this request. Returns nil sets (no filtering)
// when there is genuinely no user on the context (warming/edge callers) — NEVER
// another user's set. nil receiver → no filtering.
func (f *userBlockFilter) currentUserBlocks(c *gin.Context) (tmdb, keyword map[int64]struct{}) {
	if f == nil {
		return nil, nil
	}
	username := c.GetString(middleware.UsernameContextKey)
	if username == "" {
		return nil, nil
	}
	var uid int64
	if username == "api-key" {
		id, err := f.users.FirstAdminID(c.Request.Context()) // api-key == seed admin (mig-058 owner)
		if err != nil {
			return nil, nil
		}
		uid = id
	} else {
		u, err := f.users.GetByUsername(c.Request.Context(), username)
		if err != nil {
			f.log.WarnContext(c.Request.Context(), "discover.blocklist.user_unresolved",
				slog.String("username", username), slog.String("error", err.Error()))
			return nil, nil
		}
		uid = int64(u.ID) // admin.User.ID is uint
	}
	tmdbIDs, kwIDs, err := f.loader.LoadBlockSets(c.Request.Context(), uid)
	if err != nil {
		f.log.WarnContext(c.Request.Context(), "discover.blocklist.load_failed",
			slog.Int64("user_id", uid), slog.String("error", err.Error()))
		return nil, nil
	}
	return toSet(tmdbIDs), toSet(kwIDs)
}

// applyUserBlocks subtracts the current user's tmdb-id blocks then keyword
// blocks from a RAW page. Both nil → items unchanged. Keyword lookup is batched
// (one query for the whole page) and fail-open (a lookup error logs + skips the
// keyword filter, never 500s the grid). nil receiver → items unchanged.
func (f *userBlockFilter) applyUserBlocks(ctx context.Context, items []disco.Item, tmdbSet, kwSet map[int64]struct{}) []disco.Item {
	out := filterBlockedTMDB(items, tmdbSet)
	if f == nil || f.keywords == nil || len(kwSet) == 0 || len(out) == 0 {
		return out
	}
	kwByTMDB, err := f.keywords.ResultKeywords(ctx, tmdbIDsOf(out))
	if err != nil {
		f.log.WarnContext(ctx, "discover.keyword_filter.skipped", slog.String("error", err.Error()))
		return out
	}
	return filterBlockedKeywords(out, kwByTMDB, kwSet)
}

// toSet builds a lookup set (nil for empty → cheap pass-through downstream).
func toSet(ids []int64) map[int64]struct{} {
	if len(ids) == 0 {
		return nil
	}
	s := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

// filterBlockedTMDB returns a NEW slice with items whose TMDBID is in blocked
// removed. nil/empty blocked → items unchanged (same backing array). Items with
// a nil TMDBID (Sonarr-only stubs) are never blocked by kind=tmdb.
func filterBlockedTMDB(items []disco.Item, blocked map[int64]struct{}) []disco.Item {
	if len(items) == 0 || len(blocked) == 0 {
		return items
	}
	out := make([]disco.Item, 0, len(items))
	for _, it := range items {
		if it.TMDBID != nil {
			if _, hit := blocked[int64(*it.TMDBID)]; hit {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

// filterBlockedKeywords drops items whose TMDB keyword-ids intersect blockedKW.
// Items with no keyword row (un-enriched) are kept (accepted per-user leak,
// ADR-0020 §96).
func filterBlockedKeywords(items []disco.Item, kwByTMDB map[int64][]int64, blockedKW map[int64]struct{}) []disco.Item {
	if len(items) == 0 || len(blockedKW) == 0 || len(kwByTMDB) == 0 {
		return items
	}
	out := make([]disco.Item, 0, len(items))
	for _, it := range items {
		drop := false
		if it.TMDBID != nil {
			for _, kw := range kwByTMDB[int64(*it.TMDBID)] {
				if _, hit := blockedKW[kw]; hit {
					drop = true
					break
				}
			}
		}
		if !drop {
			out = append(out, it)
		}
	}
	return out
}

// tmdbIDsOf collects the non-nil tmdb ids of a page (dedup not required — IN() is fine).
func tmdbIDsOf(items []disco.Item) []int64 {
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		if it.TMDBID != nil {
			ids = append(ids, int64(*it.TMDBID))
		}
	}
	return ids
}

// NewUserBlockFilterForWiring builds the shared per-user blocklist filter.
// Exported entrypoint for the wiring package (userBlockFilter is unexported).
func NewUserBlockFilterForWiring(users dataports.UserRepository, loader app.BlocklistLoader, keywords ResultKeywords, log *slog.Logger) *userBlockFilter {
	return newUserBlockFilter(users, loader, keywords, log)
}
