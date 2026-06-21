package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

func TestGrabRepository_Parsed_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewGrabRepository(db)

			now := time.Now().UTC().Truncate(time.Second)
			parsed := &grab.Parsed{
				Codec: "HEVC", Source: "WEB-DL", Quality: "WEBDL-2160p", Resolution: 2160,
				HDRFlags: []string{"HDR10+", "DV"}, Dub: "Original",
				Languages: []string{"Russian", "English"}, Subs: []string{"RUS"},
				ReleaseGroup: "NTb",
			}
			rec := grab.Record{
				ID: uuid.New(), InstanceName: "alpha", SeriesID: 1, SeasonNumber: 1,
				ReleaseGUID: "g", ReleaseTitle: "t", Status: grab.StatusGrabbed,
				ScanRunID: uuid.New(), Parsed: parsed, ParsedAt: &now,
				CreatedAt: now, UpdatedAt: now,
			}
			if err := r.Create(context.Background(), rec); err != nil {
				t.Fatalf("create: %v", err)
			}

			list, _, err := r.List(context.Background(), ports.GrabFilter{}, ports.Pagination{Limit: 10})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(list) != 1 {
				t.Fatalf("list len=%d", len(list))
			}
			got := list[0]
			if got.Parsed == nil {
				t.Fatal("Parsed = nil")
			}
			if got.Parsed.Codec != "HEVC" || got.Parsed.Resolution != 2160 ||
				got.Parsed.Dub != "Original" || len(got.Parsed.HDRFlags) != 2 ||
				len(got.Parsed.Languages) != 2 || len(got.Parsed.Subs) != 1 {
				t.Fatalf("parsed round-trip mismatch: %+v", got.Parsed)
			}
			if got.ParsedAt == nil || !got.ParsedAt.Equal(now) {
				t.Fatalf("ParsedAt mismatch: %v", got.ParsedAt)
			}
		})
	}
}

func TestGrabRepository_Parsed_AbsentRow_StaysNil(t *testing.T) {
	t.Parallel()
	for _, backend := range grabBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewGrabRepository(db)

			now := time.Now().UTC().Truncate(time.Second)
			rec := grab.Record{
				ID: uuid.New(), InstanceName: "alpha", SeriesID: 1, SeasonNumber: 1,
				ReleaseGUID: "g", ReleaseTitle: "t", Status: grab.StatusGrabbed,
				ScanRunID: uuid.New(), CreatedAt: now, UpdatedAt: now,
			}
			if err := r.Create(context.Background(), rec); err != nil {
				t.Fatalf("create: %v", err)
			}
			list, _, err := r.List(context.Background(), ports.GrabFilter{}, ports.Pagination{Limit: 10})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if list[0].Parsed != nil {
				t.Fatalf("expected nil Parsed for absent row, got %+v", list[0].Parsed)
			}
			if list[0].ParsedAt != nil {
				t.Fatalf("expected nil ParsedAt, got %v", list[0].ParsedAt)
			}
		})
	}
}
