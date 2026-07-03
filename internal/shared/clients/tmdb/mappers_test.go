package tmdb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
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
	// S-E3a — canon no longer carries a localizable Title; the display
	// title lives in series_texts. Canon keeps original_title (a fact).
	if c.OriginalTitle == nil || *c.OriginalTitle != "Breaking Bad" {
		t.Errorf("original_title = %v", c.OriginalTitle)
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

	// Story 307: assert the 3 new fields surface on at least one
	// credit. The fixture contains "Production" department entries
	// (`grep '"department": "Production"' person_17419.json` → 2+
	// hits) and every credit row carries an original_name /
	// original_title + vote_count.
	var sawDept, sawOriginalTitle, sawVotes bool
	for _, c := range credits {
		if c.Department != nil && *c.Department == "Production" {
			sawDept = true
		}
		if c.OriginalTitle != nil && *c.OriginalTitle != "" {
			sawOriginalTitle = true
		}
		if c.TMDBVotes != nil && *c.TMDBVotes > 0 {
			sawVotes = true
		}
	}
	if !sawDept {
		t.Error("expected at least one credit with Department=\"Production\"")
	}
	if !sawOriginalTitle {
		t.Error("expected at least one credit with non-empty OriginalTitle")
	}
	if !sawVotes {
		t.Error("expected at least one credit with non-zero TMDBVotes")
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

func TestMapTVToCanon_OriginCountries(t *testing.T) {
	t.Parallel()
	tv := &TVResponse{
		ID:            123,
		Name:          "Multi-country",
		OriginCountry: []string{"US", "GB", "JP"},
	}
	c := MapTVToCanon(tv)
	if c.OriginCountry == nil {
		t.Error("OriginCountry should be populated from first element")
	} else if *c.OriginCountry != "US" {
		t.Errorf("OriginCountry = %q, want US", *c.OriginCountry)
	}
	if c.OriginCountries == nil || len(c.OriginCountries) != 3 {
		t.Errorf("OriginCountries = %v, want [US GB JP]", c.OriginCountries)
	} else if c.OriginCountries[0] != "US" || c.OriginCountries[1] != "GB" || c.OriginCountries[2] != "JP" {
		t.Errorf("OriginCountries = %v, want [US GB JP]", c.OriginCountries)
	}
}

func TestMapTVToCanon_OriginCountriesEmpty(t *testing.T) {
	t.Parallel()
	tv := &TVResponse{ID: 124, Name: "No country", OriginCountry: nil}
	c := MapTVToCanon(tv)
	if c.OriginCountry != nil {
		t.Errorf("OriginCountry should be nil, got %v", c.OriginCountry)
	}
	if c.OriginCountries != nil {
		t.Errorf("OriginCountries should be nil, got %v", c.OriginCountries)
	}
}

// TestPersonResponse_TranslationsParse proves the append_to_response=
// translations sub-resource on /person/{id} decodes into PersonResponse
// (S-H all-langs biography source). One GetPerson yields both en + ru bios.
func TestPersonResponse_TranslationsParse(t *testing.T) {
	p := loadPerson(t, "person_17419.json")
	if p.Translations == nil {
		t.Fatal("PersonResponse.Translations must be non-nil after append_to_response=translations")
	}
	byLang := make(map[string]string, len(p.Translations.Translations))
	for _, tr := range p.Translations.Translations {
		byLang[tr.ISO6391] = tr.Data.Biography
	}
	if byLang["en"] == "" {
		t.Errorf("en biography must be present, got %q", byLang["en"])
	}
	if byLang["ru"] == "" {
		t.Errorf("ru biography must be present, got %q", byLang["ru"])
	}
	if byLang["en"] == byLang["ru"] {
		t.Error("en and ru biographies must differ (proves per-lang decode, not a shared root)")
	}
}
