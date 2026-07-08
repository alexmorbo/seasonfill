package rest

import (
	"math"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// castSort is the server-side cast ordering key. Story 1087b moved the 1087a
// client-side sort onto the backend so "credit" (billing order) works against
// the new person_credits.credit_order column and so a future "last_appearance"
// (1087b-2) can aggregate server-side.
type castSort string

const (
	castSortEpisodes castSort = "episodes" // episode_count DESC, nulls last (default; = detail strip)
	castSortCredit   castSort = "credit"   // credit_order ASC, nulls last
	castSortName     castSort = "name"     // localized display-name collation ASC
)

// parseCastSort reads the optional ?sort= query param. Absent / unknown /
// "episodes" => castSortEpisodes. MUST stay in lockstep with the ETag
// middleware's own sort normalization (internal/shared/http/edge/etag.go) —
// the two packages cannot import each other (edge -> seriesdetailrest cycle),
// so the parse is duplicated intentionally. Story 1087b.
func parseCastSort(c *gin.Context) castSort {
	switch strings.ToLower(strings.TrimSpace(c.Query("sort"))) {
	case string(castSortCredit):
		return castSortCredit
	case string(castSortName):
		return castSortName
	default:
		return castSortEpisodes
	}
}

// sortCastMembers sorts members IN PLACE per the selected key with a
// deterministic person_id ASC tie-break, so the ordering is stable across
// fetches and an If-None-Match body always matches its ETag. Sorts the
// resolved DTO Name (exactly what the client renders). Crew is never sorted
// here (stays in the composer's department/name order).
func sortCastMembers(members []dto.CastPageMember, s castSort, lang string) {
	coll := collate.New(languageOrDefault(lang))
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i], members[j]
		switch s {
		case castSortCredit:
			ao, bo := creditOrderOrMax(a.CreditOrder), creditOrderOrMax(b.CreditOrder)
			if ao != bo {
				return ao < bo // ASC, nulls (MaxInt) last
			}
		case castSortName:
			if d := coll.CompareString(a.Name, b.Name); d != 0 {
				return d < 0
			}
		default: // castSortEpisodes
			ae, be := episodeCountOrNeg(a.EpisodeCount), episodeCountOrNeg(b.EpisodeCount)
			if ae != be {
				return ae > be // DESC, nulls (-1) last
			}
		}
		return a.PersonID < b.PersonID
	})
}

// creditOrderOrMax maps a nil billing order to MaxInt so nulls sort AFTER
// every real order (>= 0) in an ASC ordering.
func creditOrderOrMax(v *int) int {
	if v == nil {
		return math.MaxInt
	}
	return *v
}

// episodeCountOrNeg maps a nil episode count to -1 so nulls sort AFTER every
// real count (>= 0) in a DESC ordering. Mirrors the composer-side helper of
// the same name (package seriesdetail) and the FE's `?? -1`.
func episodeCountOrNeg(v *int) int {
	if v == nil {
		return -1
	}
	return *v
}

// languageOrDefault parses a BCP-47 tag for the collator, defaulting to
// English on empty/unparseable input.
func languageOrDefault(lang string) language.Tag {
	if strings.TrimSpace(lang) == "" {
		return language.English
	}
	t, err := language.Parse(lang)
	if err != nil {
		return language.English
	}
	return t
}
