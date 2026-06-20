package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestGrabRepository_UpdateParsed_HappyPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
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

			parsed := &grab.Parsed{Codec: "HEVC", HDRFlags: []string{"HDR10"}, Languages: []string{"Russian"}}
			if err := r.UpdateParsed(context.Background(), rec.ID, parsed, now); err != nil {
				t.Fatalf("update: %v", err)
			}

			list, _, err := r.List(context.Background(), ports.GrabFilter{}, ports.Pagination{Limit: 10})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if list[0].Parsed == nil || list[0].Parsed.Codec != "HEVC" {
				t.Fatalf("parsed not persisted: %+v", list[0].Parsed)
			}
		})
	}
}

func TestGrabRepository_UpdateParsed_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewGrabRepository(db)
			err := r.UpdateParsed(context.Background(), uuid.New(), &grab.Parsed{}, time.Now())
			if !errors.Is(err, ports.ErrNotFound) {
				t.Fatalf("err=%v want ErrNotFound", err)
			}
		})
	}
}

func TestGrabRepository_ListUnparsedSince(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			r := NewGrabRepository(db)

			now := time.Now().UTC().Truncate(time.Second)
			mk := func(secsAgo int, withParse bool) grab.Record {
				rec := grab.Record{
					ID: uuid.New(), InstanceName: "a", SeriesID: 1, SeasonNumber: 1,
					ReleaseGUID: "g", ReleaseTitle: "t", Status: grab.StatusGrabbed,
					ScanRunID: uuid.New(),
					CreatedAt: now.Add(-time.Duration(secsAgo) * time.Second),
					UpdatedAt: now,
				}
				if withParse {
					rec.ParsedAt = &now
				}
				return rec
			}

			for _, rec := range []grab.Record{
				mk(60, false),   // in window, unparsed → included
				mk(120, true),   // in window, parsed → excluded
				mk(3600, false), // out of window → excluded
			} {
				if err := r.Create(context.Background(), rec); err != nil {
					t.Fatalf("create: %v", err)
				}
			}

			got, err := r.ListUnparsedSince(context.Background(), now.Add(-10*time.Minute), 100)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 unparsed in-window row, got %d", len(got))
			}
		})
	}
}
