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
