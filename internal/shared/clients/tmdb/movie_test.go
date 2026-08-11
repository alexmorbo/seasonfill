package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClient_GetMovie_PathAppendLanguage asserts GetMovie hits /movie/{id} with
// the canonical append_to_response, the request language honours c.languageFor
// (a non-default lang passes through — #1184 guard), and include_image_language
// is derived from the request lang.
func TestClient_GetMovie_PathAppendLanguage(t *testing.T) {
	var gotPath, gotLang, gotAppend, gotImgLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotLang = r.URL.Query().Get("language")
		gotAppend = r.URL.Query().Get("append_to_response")
		gotImgLang = r.URL.Query().Get("include_image_language")
		_, _ = w.Write([]byte(`{"id":693134,"title":"Dune: Part Two"}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	_, err := c.GetMovie(context.Background(), 693134, "ru-RU")
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if gotPath != "/movie/693134" {
		t.Fatalf("path = %q want /movie/693134", gotPath)
	}
	if gotLang != "ru-RU" {
		t.Fatalf("language = %q want ru-RU (languageFor passthrough)", gotLang)
	}
	if gotAppend != movieAppendToResponse {
		t.Fatalf("append_to_response = %q want %q", gotAppend, movieAppendToResponse)
	}
	if !strings.Contains(gotImgLang, "ru") || !strings.Contains(gotImgLang, "null") {
		t.Fatalf("include_image_language = %q want ru,...,null", gotImgLang)
	}
}

// TestClient_GetMovie_DefaultLanguage asserts an empty language collapses to the
// client default (en-US) — the languageFor("") path.
func TestClient_GetMovie_DefaultLanguage(t *testing.T) {
	var gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLang = r.URL.Query().Get("language")
		_, _ = w.Write([]byte(`{"id":1,"title":"x"}`))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	if _, err := c.GetMovie(context.Background(), 1, ""); err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if gotLang != "en-US" {
		t.Fatalf("language = %q want en-US (default)", gotLang)
	}
}

// TestClient_GetMovie_DecodeAndMap decodes a rich fixture and asserts
// MapMovieToCanon extracts the canon fields, derives Year from release_date,
// reads imdb_id (top-level), digital/physical dates from release_dates (US
// preferred), and leaves the OMDb-owned columns nil (so the COALESCE Upsert
// preserves any OMDb value).
func TestClient_GetMovie_DecodeAndMap(t *testing.T) {
	const body = `{
      "id": 693134,
      "imdb_id": "tt15239678",
      "title": "Дюна: Часть вторая",
      "original_title": "Dune: Part Two",
      "overview": "ov",
      "tagline": "tg",
      "status": "Released",
      "release_date": "2024-02-27",
      "runtime": 167,
      "budget": 190000000,
      "revenue": 711000000,
      "original_language": "en",
      "production_countries": [{"iso_3166_1":"US","name":"United States"}],
      "popularity": 123.4,
      "vote_average": 8.2,
      "vote_count": 5000,
      "poster_path": "/p.jpg",
      "backdrop_path": "/b.jpg",
      "belongs_to_collection": {"id": 726871, "name": "Dune Collection"},
      "release_dates": {"results": [
        {"iso_3166_1":"GB","release_dates":[{"type":4,"release_date":"2024-04-01T00:00:00.000Z"}]},
        {"iso_3166_1":"US","release_dates":[
          {"type":3,"release_date":"2024-02-27T00:00:00.000Z"},
          {"type":4,"release_date":"2024-03-12T00:00:00.000Z"},
          {"type":5,"release_date":"2024-05-14T00:00:00.000Z"}]}
      ]}
    }`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "tk")
	defer c.Close()

	resp, err := c.GetMovie(context.Background(), 693134, "ru-RU")
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	canon := MapMovieToCanon(resp)

	if canon.TMDBID == nil || int64(*canon.TMDBID) != 693134 {
		t.Fatalf("tmdb_id = %v want 693134", canon.TMDBID)
	}
	if canon.Hydration != "full" {
		t.Fatalf("hydration = %q want full", canon.Hydration)
	}
	if canon.Title != "Дюна: Часть вторая" {
		t.Fatalf("title = %q (localized)", canon.Title)
	}
	if canon.IMDBID == nil || string(*canon.IMDBID) != "tt15239678" {
		t.Fatalf("imdb_id = %v want tt15239678", canon.IMDBID)
	}
	if canon.Year == nil || *canon.Year != 2024 {
		t.Fatalf("year = %v want 2024", canon.Year)
	}
	if canon.RuntimeMinutes == nil || *canon.RuntimeMinutes != 167 {
		t.Fatalf("runtime = %v want 167", canon.RuntimeMinutes)
	}
	if canon.CollectionID == nil || *canon.CollectionID != 726871 {
		t.Fatalf("collection_id = %v want 726871", canon.CollectionID)
	}
	if canon.DigitalReleaseDate == nil || canon.DigitalReleaseDate.Format("2006-01-02") != "2024-03-12" {
		t.Fatalf("digital = %v want US 2024-03-12 (US preferred over GB)", canon.DigitalReleaseDate)
	}
	if canon.PhysicalReleaseDate == nil || canon.PhysicalReleaseDate.Format("2006-01-02") != "2024-05-14" {
		t.Fatalf("physical = %v want 2024-05-14", canon.PhysicalReleaseDate)
	}
	// OMDb-owned columns MUST stay nil (COALESCE-preserve invariant).
	if canon.IMDBRating != nil || canon.IMDBVotes != nil || canon.OMDBRated != nil || canon.OMDBAwards != nil {
		t.Fatalf("OMDb columns must be nil from TMDB map: rating=%v votes=%v rated=%v awards=%v",
			canon.IMDBRating, canon.IMDBVotes, canon.OMDBRated, canon.OMDBAwards)
	}
	// tmdb_changed_at MUST stay nil (sole-writer is the changes marker).
	if canon.TMDBChangedAt != nil {
		t.Fatalf("tmdb_changed_at must be nil from TMDB map, got %v", canon.TMDBChangedAt)
	}
}

// TestMapMovieToCanon_Nil returns a zero canon for a nil response.
func TestMapMovieToCanon_Nil(t *testing.T) {
	if got := MapMovieToCanon(nil); got.TMDBID != nil || got.Title != "" {
		t.Fatalf("MapMovieToCanon(nil) = %+v want zero", got)
	}
}
