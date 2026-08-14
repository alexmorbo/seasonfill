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

// movieCastSort is the server-side movie cast ordering key. Slim duplicate of
// seriesdetail/rest cast_sort.go — movies only need credit + name (movie
// person_credits carry no episode_count / last_appearance_season). Default is
// CREDIT (not the series "episodes"): see story F2.1 §D2 for the ETag-safety
// rationale behind collapsing episodes/last_appearance onto credit.
type movieCastSort string

const (
	movieCastSortCredit movieCastSort = "credit" // credit_order ASC, nulls last (default)
	movieCastSortName   movieCastSort = "name"   // localized display-name collation ASC
)

// parseMovieCastSort reads ?sort=. Only "name" switches off the credit default;
// every other value (absent, "episodes", "last_appearance", unknown) → credit.
// MUST stay consistent with edge/etag.go's sort key folding (F2.1 §D2).
func parseMovieCastSort(c *gin.Context) movieCastSort {
	if strings.EqualFold(strings.TrimSpace(c.Query("sort")), string(movieCastSortName)) {
		return movieCastSortName
	}
	return movieCastSortCredit
}

// sortMovieCast sorts members IN PLACE with a deterministic person_id ASC
// tie-break, so the order is stable across fetches and an If-None-Match body
// always matches its ETag.
func sortMovieCast(members []dto.MovieCastMember, s movieCastSort, lang string) {
	coll := collate.New(languageOrDefault(lang))
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i], members[j]
		switch s {
		case movieCastSortName:
			if d := coll.CompareString(a.Name, b.Name); d != 0 {
				return d < 0
			}
		default: // movieCastSortCredit
			ao, bo := creditOrderOrMax(a.CreditOrder), creditOrderOrMax(b.CreditOrder)
			if ao != bo {
				return ao < bo // ASC, nulls (MaxInt) last
			}
		}
		return a.PersonID < b.PersonID
	})
}

// creditOrderOrMax maps a nil billing order to MaxInt so nulls sort AFTER every
// real order in an ASC ordering.
func creditOrderOrMax(v *int) int {
	if v == nil {
		return math.MaxInt
	}
	return *v
}

// languageOrDefault parses a BCP-47 tag for the collator, defaulting to English
// on empty/unparseable input.
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
