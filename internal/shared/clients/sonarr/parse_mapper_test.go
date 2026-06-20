package sonarr_test

import (
	"reflect"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

func TestMergeParse_HappyPath(t *testing.T) {
	t.Parallel()
	pr := sonarr.ParseResult{
		Quality:      "WEBDL-2160p",
		Source:       "WEB-DL",
		Resolution:   2160,
		Languages:    []string{"Russian", "English"},
		ReleaseGroup: "NTb",
	}
	ex := sonarr.Extras{
		Codec: "HEVC", HDRFlags: []string{"HDR10+", "DV"},
		Dub: "Original", Subs: []string{"RUS"},
	}

	got := sonarr.MergeParse(pr, ex)

	if got.Codec != "HEVC" || got.Source != "WEB-DL" || got.Quality != "WEBDL-2160p" ||
		got.Resolution != 2160 || got.Dub != "Original" || got.ReleaseGroup != "NTb" {
		t.Fatalf("scalar mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.HDRFlags, []string{"HDR10+", "DV"}) {
		t.Fatalf("HDRFlags=%v", got.HDRFlags)
	}
	if !reflect.DeepEqual(got.Languages, []string{"Russian", "English"}) {
		t.Fatalf("Languages=%v", got.Languages)
	}
	if !reflect.DeepEqual(got.Subs, []string{"RUS"}) {
		t.Fatalf("Subs=%v", got.Subs)
	}
}

func TestMergeParse_Empty(t *testing.T) {
	t.Parallel()
	got := sonarr.MergeParse(sonarr.ParseResult{}, sonarr.Extras{})
	if !got.IsZero() {
		t.Fatalf("expected IsZero, got %+v", got)
	}
	if got.HDRFlags == nil || got.Languages == nil || got.Subs == nil {
		t.Fatalf("nil slices in merged Parsed (must be non-nil empty): %+v", got)
	}
}

func TestMergeParse_DeduplicatesAndTrims(t *testing.T) {
	t.Parallel()
	got := sonarr.MergeParse(
		sonarr.ParseResult{Languages: []string{"Russian", " Russian ", "English", ""}},
		sonarr.Extras{HDRFlags: []string{"HDR10", "HDR10", "DV"}, Subs: []string{"RUS", "RUS"}},
	)
	if !reflect.DeepEqual(got.Languages, []string{"Russian", "English"}) ||
		!reflect.DeepEqual(got.HDRFlags, []string{"HDR10", "DV"}) ||
		!reflect.DeepEqual(got.Subs, []string{"RUS"}) {
		t.Fatalf("dedup mismatch: %+v", got)
	}
}
