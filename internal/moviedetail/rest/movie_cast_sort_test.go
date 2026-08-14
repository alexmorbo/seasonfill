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

func mcMember(id int64, name string, credit *int) dto.MovieCastMember {
	return dto.MovieCastMember{PersonID: id, Name: name, CreditOrder: credit}
}

func mcIDs(ms []dto.MovieCastMember) []int64 {
	out := make([]int64, len(ms))
	for i, m := range ms {
		out[i] = m.PersonID
	}
	return out
}

func mcGinContext(rawURL string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(context.Background(), "GET", rawURL, nil)
	return c
}

func TestSortMovieCast(t *testing.T) {
	t.Parallel()
	base := func() []dto.MovieCastMember {
		return []dto.MovieCastMember{
			mcMember(1, "Zoe", new(2)),
			mcMember(2, "Amy", new(0)),
			mcMember(3, "Mike", new(1)),
			mcMember(4, "Bob", nil), // null credit → last
		}
	}

	t.Run("credit ASC nulls last (default)", func(t *testing.T) {
		m := base()
		sortMovieCast(m, movieCastSortCredit, "en-US")
		assert.Equal(t, []int64{2, 3, 1, 4}, mcIDs(m)) // 0,1,2,nil
	})
	t.Run("name collation ASC", func(t *testing.T) {
		m := base()
		sortMovieCast(m, movieCastSortName, "en-US")
		assert.Equal(t, []int64{2, 4, 3, 1}, mcIDs(m)) // Amy,Bob,Mike,Zoe
	})
	t.Run("deterministic person_id tie-break on equal credit", func(t *testing.T) {
		m := []dto.MovieCastMember{
			mcMember(5, "A", new(1)),
			mcMember(2, "B", new(1)),
			mcMember(9, "C", new(1)),
		}
		sortMovieCast(m, movieCastSortCredit, "en-US")
		assert.Equal(t, []int64{2, 5, 9}, mcIDs(m))
	})
}

func TestParseMovieCastSort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		query string
		want  movieCastSort
	}{
		{"", movieCastSortCredit},
		{"credit", movieCastSortCredit},
		{"episodes", movieCastSortCredit},        // meaningless for movies → credit
		{"last_appearance", movieCastSortCredit}, // meaningless for movies → credit
		{"bogus", movieCastSortCredit},
		{"name", movieCastSortName},
		{"  NAME ", movieCastSortName},
	}
	for _, tc := range cases {
		c := mcGinContext("/x?sort=" + url.QueryEscape(tc.query))
		assert.Equalf(t, tc.want, parseMovieCastSort(c), "sort=%q", tc.query)
	}
}
