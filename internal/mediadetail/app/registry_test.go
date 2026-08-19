package app

import (
	"context"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// stubPlugin is a minimal SectionPlugin for registry-order assertions.
type stubPlugin struct{ section domain.Section }

func (p stubPlugin) Coverage(context.Context, domain.MediaID, string) (int, int, error) {
	return 0, 0, nil
}
func (p stubPlugin) Staleness(context.Context, domain.MediaID, string, time.Time) (domain.SectionVerdict, error) {
	return domain.SectionVerdict{Section: p.section}, nil
}
func (p stubPlugin) Refresh(context.Context, domain.MediaID, string) error { return nil }
func (p stubPlugin) Section() domain.Section                               { return p.section }

func TestRegistryStableOrder(t *testing.T) {
	r := NewSectionRegistry()
	order := []domain.Section{domain.SectionText, domain.SectionCast, domain.SectionRecs}
	for _, s := range order {
		r.Register(domain.MediaTypeSeries, stubPlugin{section: s})
	}
	got := r.For(domain.MediaTypeSeries)
	if len(got) != len(order) {
		t.Fatalf("For len = %d, want %d", len(got), len(order))
	}
	for i, s := range order {
		if got[i].Section() != s {
			t.Errorf("For[%d] = %v, want %v", i, got[i].Section(), s)
		}
	}
}

func TestRegistryForUnknownEmpty(t *testing.T) {
	r := NewSectionRegistry()
	r.Register(domain.MediaTypeSeries, stubPlugin{section: domain.SectionText})
	got := r.For(domain.MediaTypeMovie)
	if got == nil {
		t.Fatal("For must return non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("For(movie) len = %d, want 0", len(got))
	}
}

func TestRegistryNilAndInvalidIgnored(t *testing.T) {
	r := NewSectionRegistry()
	r.Register(domain.MediaTypeSeries, nil)      // nil plugin ignored
	r.Register(domain.MediaType{}, stubPlugin{}) // invalid type ignored
	if len(r.For(domain.MediaTypeSeries)) != 0 {
		t.Error("nil plugin must be ignored")
	}
	if len(r.For(domain.MediaType{})) != 0 {
		t.Error("invalid type registration must be ignored")
	}
}

func TestRegistryForIsCopy(t *testing.T) {
	r := NewSectionRegistry()
	r.Register(domain.MediaTypeSeries, stubPlugin{section: domain.SectionText})
	got := r.For(domain.MediaTypeSeries)
	got[0] = nil // mutate the returned slice
	again := r.For(domain.MediaTypeSeries)
	if again[0] == nil {
		t.Error("For must return a defensive copy")
	}
}
