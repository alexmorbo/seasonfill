package domain

import "testing"

func TestRowEnums_IsValid(t *testing.T) {
	t.Parallel()
	valid := []RowType{RowTypeTrending, RowTypePopular, RowTypeUpcoming,
		RowTypeGenre, RowTypeNetwork, RowTypeKeyword, RowTypeWatchProvider,
		RowTypeRecentlyAdded, RowTypeUpcomingReleases}
	for _, rt := range valid {
		if !rt.IsValid() {
			t.Errorf("RowType %q should be valid", rt)
		}
	}
	if RowType("bogus").IsValid() {
		t.Error("bogus RowType must be invalid")
	}
	if !SourceTMDBDiscover.IsValid() || !SourceLibrary.IsValid() || RowSource("x").IsValid() {
		t.Error("RowSource IsValid mismatch")
	}
	if !MediaTypeTV.IsValid() || !MediaTypeMovie.IsValid() || MediaType("x").IsValid() {
		t.Error("MediaType IsValid mismatch")
	}
}

func TestDefaultRows_Invariants(t *testing.T) {
	t.Parallel()
	rows := DefaultRows()
	if len(rows) != 7 {
		t.Fatalf("DefaultRows len = %d, want 7", len(rows))
	}
	for i, r := range rows {
		if r.Position != i {
			t.Errorf("row %d position = %d, want %d (dense 0..N)", i, r.Position, i)
		}
		if !r.RowType.IsValid() || !r.Source.IsValid() || !r.MediaType.IsValid() {
			t.Errorf("row %d has invalid enum: %+v", i, r)
		}
		if r.Title == "" {
			t.Errorf("row %d missing Russian title", i)
		}
		if r.ID != 0 {
			t.Errorf("code-default row %d must have ID 0, got %d", i, r.ID)
		}
		if r.Source == SourceLibrary && r.RowType != RowTypeRecentlyAdded {
			t.Errorf("row %d: only recently_added may be source=library", i)
		}
	}
}

func TestDefaultRows_UpcomingParams(t *testing.T) {
	t.Parallel()
	rows := DefaultRows()
	byType := func(rt RowType) Row {
		for _, r := range rows {
			if r.RowType == rt {
				return r
			}
		}
		t.Fatalf("row %q not found", rt)
		return Row{}
	}
	up := byType(RowTypeUpcoming)
	if up.Params["sort_by"] != "popularity.desc" {
		t.Errorf("upcoming sort_by = %q, want popularity.desc", up.Params["sort_by"])
	}
	if up.Params["vote_count.gte"] != "10" {
		t.Errorf("upcoming vote_count.gte = %q, want 10", up.Params["vote_count.gte"])
	}
	upR := byType(RowTypeUpcomingReleases)
	if upR.Params["sort_by"] != "first_air_date.asc" {
		t.Errorf("upcoming_releases sort_by = %q, want first_air_date.asc", upR.Params["sort_by"])
	}
}
