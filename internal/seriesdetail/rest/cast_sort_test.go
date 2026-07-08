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
