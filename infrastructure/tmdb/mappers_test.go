package tmdb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
)

func loadTV(t *testing.T, name string) *TVResponse {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var out TVResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &out
}

func loadSeason(t *testing.T, name string) *SeasonResponse {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var out SeasonResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &out
}

func loadPerson(t *testing.T, name string) *PersonResponse {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var out PersonResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &out
}

func loadFind(t *testing.T, name string) *FindResponse {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var out FindResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &out
}

func TestMapTVToCanon_BreakingBad(t *testing.T) {
	c := MapTVToCanon(loadTV(t, "tv_1396.json"))
	if c.Title != "Breaking Bad" {
		t.Errorf("title = %q", c.Title)
	}
	if c.TMDBID == nil || *c.TMDBID != 1396 {
		t.Errorf("tmdb_id = %v", c.TMDBID)
	}
	if c.Hydration != series.HydrationFull {
		t.Errorf("hydration = %q", c.Hydration)
	}
	// PRD §5.4: TVDB id MUST come through from external_ids.
	if c.TVDBID == nil {
		t.Errorf("tvdb_id should populate from external_ids")
	}
	// Hash normalisation invariant.
	if c.IMDBID == nil || (*c.IMDBID)[:2] != "tt" {
		t.Errorf("imdb_id should have tt prefix: %v", c.IMDBID)
	}
}

func TestMapTVToCredits_SplitCastCrew(t *testing.T) {
	creds, stubs := MapTVToCredits(loadTV(t, "tv_1396.json"))
	if len(creds) == 0 || len(stubs) == 0 {
		t.Fatalf("expected non-empty creds + stubs; got %d / %d", len(creds), len(stubs))
	}
	var cast, crew int
	for _, c := range creds {
		switch c.Kind {
		case people.SeriesCreditCast:
			cast++
		case people.SeriesCreditCrew:
			crew++
		}
	}
	if cast == 0 || crew == 0 {
		t.Fatalf("expected both cast (%d) and crew (%d)", cast, crew)
	}
	// All stubs must be HydrationStub.
	for _, s := range stubs {
		if s.Hydration != people.HydrationStub {
			t.Errorf("person stub hydration = %q", s.Hydration)
		}
		if s.Name == "" {
			t.Errorf("person stub missing Name")
		}
	}
}

func TestMapTVToTaxonomy_FourSlices(t *testing.T) {
	genres, keywords, networks, companies := MapTVToTaxonomy(loadTV(t, "tv_1396.json"))
	if len(genres) == 0 {
		t.Error("expected non-empty genres")
	}
	if len(networks) == 0 {
		t.Error("expected non-empty networks")
	}
	// keywords / companies may be empty depending on TMDB payload —
	// just confirm no panic.
	_ = keywords
	_ = companies
}

func TestMapTVToRecommendations_Stubs(t *testing.T) {
	recs := MapTVToRecommendations(loadTV(t, "tv_1396.json"))
	if len(recs) == 0 {
		t.Skip("recommendations empty in fixture")
	}
	for _, r := range recs {
		if r.Hydration != series.HydrationStub {
			t.Errorf("rec hydration = %q (expected stub)", r.Hydration)
		}
		if r.TMDBID == nil {
			t.Error("rec missing tmdb_id")
		}
	}
}

func TestMapSeasonToEpisodes(t *testing.T) {
	eps := MapSeasonToEpisodes(loadSeason(t, "season_1396_1.json"), 42, 99)
	if len(eps) == 0 {
		t.Fatal("expected non-empty episodes")
	}
	for _, e := range eps {
		if e.SeriesID != 42 {
			t.Errorf("series_id = %d", e.SeriesID)
		}
		if e.SeasonID == nil || *e.SeasonID != 99 {
			t.Errorf("season_id = %v", e.SeasonID)
		}
		if e.EpisodeNumber == 0 {
			t.Errorf("episode_number not propagated")
		}
		// Sonarr-owned fields MUST stay nil.
		if e.SonarrEpisodeID != nil {
			t.Errorf("sonarr_episode_id leaked: %v", e.SonarrEpisodeID)
		}
	}
}

func TestMapSeasonToCredits_GuestStarsAndCrew(t *testing.T) {
	creds := MapSeasonToCredits(loadSeason(t, "season_1396_1.json"))
	if len(creds) == 0 {
		t.Skip("season fixture has no guest stars / crew — re-capture")
	}
	var gs, crew int
	for _, c := range creds {
		switch c.Kind {
		case people.EpisodeCreditGuestStar:
			gs++
		case people.EpisodeCreditCrew:
			crew++
		}
	}
	if gs+crew == 0 {
		t.Errorf("expected guest_star or crew rows")
	}
}

func TestMapPersonToDomain_BryanCranston(t *testing.T) {
	p, credits := MapPersonToDomain(loadPerson(t, "person_17419.json"))
	if p.Name != "Bryan Cranston" {
		t.Errorf("name = %q", p.Name)
	}
	if p.Hydration != people.HydrationFull {
		t.Errorf("hydration = %q", p.Hydration)
	}
	if p.Biography == "" {
		t.Error("biography empty")
	}
	// At least one tv credit + one movie credit.
	var tv, mov int
	for _, c := range credits {
		switch c.MediaType {
		case MediaTypeTV:
			tv++
		case MediaTypeMovie:
			mov++
		}
	}
	if tv == 0 || mov == 0 {
		t.Errorf("expected both tv (%d) and movie (%d) credits", tv, mov)
	}
}

func TestMapFindResponseToTMDBID(t *testing.T) {
	id, ok := MapFindResponseToTMDBID(loadFind(t, "find_tvdb_81189.json"))
	if !ok {
		t.Fatal("expected ok=true for tvdb 81189")
	}
	if id != 1396 {
		t.Errorf("expected Breaking Bad tmdb id 1396, got %d", id)
	}

	// Empty payload returns false.
	if _, ok := MapFindResponseToTMDBID(&FindResponse{}); ok {
		t.Error("empty response returned ok=true")
	}
}

func TestNormaliseIMDBID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"tt0944947", "tt0944947"},
		{"0944947", "tt0944947"},
		{"  944947 ", "tt944947"},
		{"nm0186505", "nm0186505"}, // person id passes through (has tt-or-nm-or-other prefix)
		{"garbage", "garbage"},
	}
	for _, tc := range cases {
		if got := NormaliseIMDBID(tc.in); got != tc.want {
			t.Errorf("NormaliseIMDBID(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}
