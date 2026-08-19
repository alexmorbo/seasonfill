package domain

import (
	"errors"
	"testing"

	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func TestParseMediaType(t *testing.T) {
	cases := []struct {
		in      string
		want    MediaType
		wantErr bool
	}{
		{"series", MediaTypeSeries, false},
		{"movie", MediaTypeMovie, false},
		{"  Series ", MediaTypeSeries, false},
		{"MOVIE", MediaTypeMovie, false},
		{"", MediaType{}, true},
		{"person", MediaType{}, true},
		{"tv", MediaType{}, true},
	}
	for _, c := range cases {
		got, err := ParseMediaType(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMediaType(%q): want error, got nil", c.in)
			}
			if !errors.Is(err, ErrInvalidMediaType) {
				t.Errorf("ParseMediaType(%q): want ErrInvalidMediaType, got %v", c.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMediaType(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseMediaType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMediaTypeStringValidZero(t *testing.T) {
	if MediaTypeSeries.String() != "series" || MediaTypeMovie.String() != "movie" {
		t.Fatalf("unexpected String(): %q %q", MediaTypeSeries.String(), MediaTypeMovie.String())
	}
	if !MediaTypeSeries.Valid() || !MediaTypeMovie.Valid() {
		t.Fatal("known types must be Valid")
	}
	var zero MediaType
	if zero.Valid() {
		t.Fatal("zero MediaType must be invalid")
	}
	if !zero.IsZero() {
		t.Fatal("zero MediaType must be IsZero")
	}
	if zero.String() != "" {
		t.Fatalf("zero String() = %q, want empty", zero.String())
	}
}

func TestParseSectionFullEnum(t *testing.T) {
	if len(CanonicalSections) != 8 {
		t.Fatalf("CanonicalSections len = %d, want 8", len(CanonicalSections))
	}
	for _, want := range CanonicalSections {
		got, err := ParseSection(want.String())
		if err != nil {
			t.Errorf("ParseSection(%q): unexpected error %v", want.String(), err)
		}
		if got != want {
			t.Errorf("ParseSection(%q) = %v, want %v", want.String(), got, want)
		}
		if !want.Valid() || want.IsZero() {
			t.Errorf("canonical section %q must be Valid and non-zero", want.String())
		}
	}
	// Normalization.
	if got, err := ParseSection("  HERO "); err != nil || got != SectionHero {
		t.Errorf("ParseSection(normalize) = %v, %v", got, err)
	}
}

func TestParseSectionUnknown(t *testing.T) {
	for _, in := range []string{"", "overview", "recommendations", "skeleton", "bogus"} {
		if _, err := ParseSection(in); !errors.Is(err, ErrInvalidSection) {
			t.Errorf("ParseSection(%q): want ErrInvalidSection, got %v", in, err)
		}
	}
	var zero Section
	if zero.Valid() || !zero.IsZero() {
		t.Fatal("zero Section must be invalid and IsZero")
	}
}

func TestLegacySeriesSectionMap(t *testing.T) {
	want := map[string]Section{
		"overview":        SectionText,
		"recommendations": SectionRecs,
		"skeleton":        SectionHero,
		"cast":            SectionCast,
		"media":           SectionMedia,
	}
	for k, v := range want {
		if LegacySeriesSectionMap[k] != v {
			t.Errorf("LegacySeriesSectionMap[%q] = %v, want %v", k, LegacySeriesSectionMap[k], v)
		}
	}
}

func TestNewMediaID(t *testing.T) {
	id, err := NewMediaID(MediaTypeMovie, 123, shareddomain.TMDBID(558449))
	if err != nil {
		t.Fatalf("NewMediaID: unexpected error %v", err)
	}
	if id.Type() != MediaTypeMovie {
		t.Errorf("Type() = %v, want movie", id.Type())
	}
	if id.InternalID() != 123 {
		t.Errorf("InternalID() = %d, want 123", id.InternalID())
	}
	if id.TMDBID() != shareddomain.TMDBID(558449) {
		t.Errorf("TMDBID() = %d, want 558449", id.TMDBID())
	}
	if !id.Valid() {
		t.Error("id must be Valid")
	}
	if id.Key() != "movie-123" {
		t.Errorf("Key() = %q, want movie-123", id.Key())
	}
}

func TestNewMediaIDInvalid(t *testing.T) {
	if _, err := NewMediaID(MediaType{}, 1, 0); !errors.Is(err, ErrInvalidMediaID) {
		t.Errorf("invalid type: want ErrInvalidMediaID, got %v", err)
	}
	if _, err := NewMediaID(MediaTypeSeries, 0, 0); !errors.Is(err, ErrInvalidMediaID) {
		t.Errorf("zero id: want ErrInvalidMediaID, got %v", err)
	}
	if _, err := NewMediaID(MediaTypeSeries, -5, 0); !errors.Is(err, ErrInvalidMediaID) {
		t.Errorf("negative id: want ErrInvalidMediaID, got %v", err)
	}
	// tmdb zero sentinel is allowed.
	if _, err := NewMediaID(MediaTypeSeries, 7, 0); err != nil {
		t.Errorf("tmdb zero should be allowed, got %v", err)
	}
	var zero MediaID
	if zero.Valid() {
		t.Error("zero MediaID must be invalid")
	}
}

func TestSectionVerdictZeroValue(t *testing.T) {
	var v SectionVerdict
	if v.Stale {
		t.Error("zero verdict must be not stale")
	}
	if !v.Section.IsZero() {
		t.Error("zero verdict section must be zero")
	}
	stale := SectionVerdict{Section: SectionText, Stale: true, Reason: "never"}
	if !stale.Stale || stale.Section != SectionText || stale.Reason != "never" {
		t.Error("SectionVerdict fields not carried")
	}
}

func TestMediaDetailIdentity(t *testing.T) {
	id, _ := NewMediaID(MediaTypeSeries, 74, 0)
	d := MediaDetail{ID: id}
	if d.Type() != MediaTypeSeries {
		t.Errorf("Type() = %v, want series", d.Type())
	}
	if d.ID != id {
		t.Error("ID not carried")
	}
}
