package rest

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

func castMember(id int64, name string, credit, eps *int) dto.CastPageMember {
	return dto.CastPageMember{PersonID: id, Name: name, CreditOrder: credit, EpisodeCount: eps}
}

func ids(ms []dto.CastPageMember) []int64 {
	out := make([]int64, len(ms))
	for i, m := range ms {
		out[i] = m.PersonID
	}
	return out
}

// ginTestContext builds a *gin.Context whose Request carries the given URL so
// c.Query("sort") resolves during parseCastSort.
func ginTestContext(rawURL string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(context.Background(), "GET", rawURL, nil)
	return c
}

func TestSortCastMembers(t *testing.T) {
	t.Parallel()
	base := func() []dto.CastPageMember {
		return []dto.CastPageMember{
			castMember(1, "Zoe", new(0), new(2)),
			castMember(2, "Amy", new(1), new(9)),
			castMember(3, "Mike", new(2), new(5)),
			castMember(4, "Bob", nil, nil), // nulls → last in every branch
		}
	}

	t.Run("episodes DESC nulls last", func(t *testing.T) {
		m := base()
		sortCastMembers(m, castSortEpisodes, "en-US")
		assert.Equal(t, []int64{2, 3, 1, 4}, ids(m)) // 9,5,2,nil
	})
	t.Run("credit ASC nulls last", func(t *testing.T) {
		m := base()
		sortCastMembers(m, castSortCredit, "en-US")
		assert.Equal(t, []int64{1, 2, 3, 4}, ids(m)) // 0,1,2,nil
	})
	t.Run("name collation ASC", func(t *testing.T) {
		m := base()
		sortCastMembers(m, castSortName, "en-US")
		assert.Equal(t, []int64{2, 4, 3, 1}, ids(m)) // Amy,Bob,Mike,Zoe
	})
	t.Run("last_appearance DESC nulls last with person_id tie-break", func(t *testing.T) {
		m := []dto.CastPageMember{
			{PersonID: 10, Name: "A", LastAppearanceSeason: new(5)},
			{PersonID: 20, Name: "B", LastAppearanceSeason: new(2)},
			{PersonID: 30, Name: "C", LastAppearanceSeason: nil}, // null → last
			{PersonID: 40, Name: "D", LastAppearanceSeason: new(5)},
		}
		sortCastMembers(m, castSortLastAppearance, "en-US")
		// season 5 (ids 10<40 by tie-break), then 2 (id 20), then nil (id 30).
		assert.Equal(t, []int64{10, 40, 20, 30}, ids(m))
	})
	t.Run("deterministic person_id tie-break", func(t *testing.T) {
		m := []dto.CastPageMember{
			castMember(5, "A", new(1), new(3)),
			castMember(2, "B", new(1), new(3)),
			castMember(9, "C", new(1), new(3)),
		}
		sortCastMembers(m, castSortEpisodes, "en-US") // all equal → id ASC
		assert.Equal(t, []int64{2, 5, 9}, ids(m))
	})
}

func TestSortCastMembersLastAppearanceEpisodeTiebreak(t *testing.T) {
	t.Parallel()

	t.Run("same season higher episode_count first", func(t *testing.T) {
		m := []dto.CastPageMember{
			{PersonID: 20, Name: "Guest", LastAppearanceSeason: new(5), EpisodeCount: new(1)},
			{PersonID: 10, Name: "Regular", LastAppearanceSeason: new(5), EpisodeCount: new(50)},
		}
		sortCastMembers(m, castSortLastAppearance, "en-US")
		// same last season 5 → episode_count DESC: 50 before 1.
		assert.Equal(t, []int64{10, 20}, ids(m))
	})

	t.Run("nil episode_count sorts after real counts within season", func(t *testing.T) {
		m := []dto.CastPageMember{
			{PersonID: 30, Name: "Unknown", LastAppearanceSeason: new(5), EpisodeCount: nil},
			{PersonID: 20, Name: "Guest", LastAppearanceSeason: new(5), EpisodeCount: new(1)},
			{PersonID: 10, Name: "Regular", LastAppearanceSeason: new(5), EpisodeCount: new(50)},
		}
		sortCastMembers(m, castSortLastAppearance, "en-US")
		// season 5 → 50, 1, then nil (nulls last).
		assert.Equal(t, []int64{10, 20, 30}, ids(m))
	})

	t.Run("full tie falls through to person_id ASC", func(t *testing.T) {
		m := []dto.CastPageMember{
			{PersonID: 30, Name: "C", LastAppearanceSeason: new(5), EpisodeCount: new(5)},
			{PersonID: 15, Name: "A", LastAppearanceSeason: new(5), EpisodeCount: new(5)},
		}
		sortCastMembers(m, castSortLastAppearance, "en-US")
		// same season, same episode_count → lower person_id first.
		assert.Equal(t, []int64{15, 30}, ids(m))
	})

	t.Run("primary season key dominates episode_count", func(t *testing.T) {
		m := []dto.CastPageMember{
			{PersonID: 20, Name: "Regular", LastAppearanceSeason: new(3), EpisodeCount: new(50)},
			{PersonID: 10, Name: "Guest", LastAppearanceSeason: new(5), EpisodeCount: new(1)},
		}
		sortCastMembers(m, castSortLastAppearance, "en-US")
		// season 5 (1 ep) still ranks above season 3 (50 eps): primary wins.
		assert.Equal(t, []int64{10, 20}, ids(m))
	})
}

func TestParseCastSort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		query string
		want  castSort
	}{
		{"", castSortEpisodes},
		{"episodes", castSortEpisodes},
		{"bogus", castSortEpisodes},
		{"credit", castSortCredit},
		{"name", castSortName},
		{"  NAME ", castSortName},
		{"last_appearance", castSortLastAppearance},
		{"  Last_Appearance ", castSortLastAppearance},
	}
	for _, tc := range cases {
		c := ginTestContext("/x?sort=" + url.QueryEscape(tc.query))
		assert.Equalf(t, tc.want, parseCastSort(c), "sort=%q", tc.query)
	}
}
