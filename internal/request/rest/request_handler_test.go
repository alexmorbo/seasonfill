package rest

import (
	"reflect"
	"testing"

	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
)

func seasonsPtr(vals ...int) *[]int {
	s := append([]int(nil), vals...)
	return &s
}

func TestToItemEnriched(t *testing.T) {
	usernames := map[uint]string{
		2: "alice",
		3: "bob",
	}
	movieTitles := map[int64]string{
		603: "The Matrix",
	}
	seriesTitles := map[int64]string{
		// deliberately the same numeric key as a movie TMDB id to prove the
		// movie/tv keyspaces do not collide.
		603: "Breaking Bad",
	}

	tests := []struct {
		name         string
		req          reqdomain.Request
		wantUsername string
		wantTitle    *string // nil → title must be omitted
		wantSeasons  *[]int  // nil → seasons must be omitted
	}{
		{
			name: "movie with resolved title omits seasons",
			req: reqdomain.Request{
				ID:        1,
				UserID:    2,
				MediaType: reqdomain.MediaTypeMovie,
				TMDBID:    603,
				Seasons:   seasonsPtr(1, 2), // must be ignored for movie rows
			},
			wantUsername: "alice",
			wantTitle:    new("The Matrix"),
			wantSeasons:  nil,
		},
		{
			name: "tv with seasons resolves title via series map",
			req: reqdomain.Request{
				ID:        2,
				UserID:    3,
				MediaType: reqdomain.MediaTypeTV,
				TMDBID:    603, // same numeric value, resolves from seriesTitles
				Seasons:   seasonsPtr(1, 2, 3),
			},
			wantUsername: "bob",
			wantTitle:    new("Breaking Bad"),
			wantSeasons:  seasonsPtr(1, 2, 3),
		},
		{
			name: "unresolved username leaves username empty",
			req: reqdomain.Request{
				ID:        3,
				UserID:    99, // absent from usernames map
				MediaType: reqdomain.MediaTypeMovie,
				TMDBID:    603,
			},
			wantUsername: "",
			wantTitle:    new("The Matrix"),
			wantSeasons:  nil,
		},
		{
			name: "unresolved movie title omits title",
			req: reqdomain.Request{
				ID:        4,
				UserID:    2,
				MediaType: reqdomain.MediaTypeMovie,
				TMDBID:    12345, // absent from movieTitles map
			},
			wantUsername: "alice",
			wantTitle:    nil,
			wantSeasons:  nil,
		},
		{
			name: "unresolved tv title still carries seasons",
			req: reqdomain.Request{
				ID:        5,
				UserID:    3,
				MediaType: reqdomain.MediaTypeTV,
				TMDBID:    777, // absent from seriesTitles map
				Seasons:   seasonsPtr(4),
			},
			wantUsername: "bob",
			wantTitle:    nil,
			wantSeasons:  seasonsPtr(4),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toItemEnriched(tt.req, usernames, movieTitles, seriesTitles)

			if got.Username != tt.wantUsername {
				t.Errorf("username = %q, want %q", got.Username, tt.wantUsername)
			}

			switch {
			case tt.wantTitle == nil && got.Title != nil:
				t.Errorf("title = %q, want nil (omitted)", *got.Title)
			case tt.wantTitle != nil && got.Title == nil:
				t.Errorf("title = nil, want %q", *tt.wantTitle)
			case tt.wantTitle != nil && *got.Title != *tt.wantTitle:
				t.Errorf("title = %q, want %q", *got.Title, *tt.wantTitle)
			}

			switch {
			case tt.wantSeasons == nil && got.Seasons != nil:
				t.Errorf("seasons = %v, want nil (omitted)", *got.Seasons)
			case tt.wantSeasons != nil && got.Seasons == nil:
				t.Errorf("seasons = nil, want %v", *tt.wantSeasons)
			case tt.wantSeasons != nil && !reflect.DeepEqual(*got.Seasons, *tt.wantSeasons):
				t.Errorf("seasons = %v, want %v", *got.Seasons, *tt.wantSeasons)
			}
		})
	}
}

func TestDistinctRequestIDs(t *testing.T) {
	tests := []struct {
		name         string
		items        []reqdomain.Request
		wantUserIDs  []uint
		wantMovieIDs []int64
		wantTVIDs    []int64
	}{
		{
			name:         "empty page yields nil slices",
			items:        nil,
			wantUserIDs:  nil,
			wantMovieIDs: nil,
			wantTVIDs:    nil,
		},
		{
			name: "dedupes repeated user and content ids preserving first-seen order",
			items: []reqdomain.Request{
				{UserID: 2, MediaType: reqdomain.MediaTypeMovie, TMDBID: 603},
				{UserID: 2, MediaType: reqdomain.MediaTypeMovie, TMDBID: 603}, // dup user + movie
				{UserID: 3, MediaType: reqdomain.MediaTypeTV, TMDBID: 1399},
				{UserID: 3, MediaType: reqdomain.MediaTypeTV, TMDBID: 1399}, // dup user + tv
				{UserID: 2, MediaType: reqdomain.MediaTypeMovie, TMDBID: 550},
			},
			wantUserIDs:  []uint{2, 3},
			wantMovieIDs: []int64{603, 550},
			wantTVIDs:    []int64{1399},
		},
		{
			name: "same numeric id across movie and tv stays in separate keyspaces",
			items: []reqdomain.Request{
				{UserID: 5, MediaType: reqdomain.MediaTypeMovie, TMDBID: 100},
				{UserID: 5, MediaType: reqdomain.MediaTypeTV, TMDBID: 100},
			},
			wantUserIDs:  []uint{5},
			wantMovieIDs: []int64{100},
			wantTVIDs:    []int64{100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userIDs, movieIDs, tvIDs := distinctRequestIDs(tt.items)
			if !reflect.DeepEqual(userIDs, tt.wantUserIDs) {
				t.Errorf("userIDs = %v, want %v", userIDs, tt.wantUserIDs)
			}
			if !reflect.DeepEqual(movieIDs, tt.wantMovieIDs) {
				t.Errorf("movieIDs = %v, want %v", movieIDs, tt.wantMovieIDs)
			}
			if !reflect.DeepEqual(tvIDs, tt.wantTVIDs) {
				t.Errorf("tvIDs = %v, want %v", tvIDs, tt.wantTVIDs)
			}
		})
	}
}
