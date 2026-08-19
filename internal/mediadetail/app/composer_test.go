package app

import (
	"context"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

func TestComposerComposeIdentity(t *testing.T) {
	id, err := domain.NewMediaID(domain.MediaTypeSeries, 74, 0)
	if err != nil {
		t.Fatalf("NewMediaID: %v", err)
	}
	c := NewComposer(nil)
	got, err := c.Compose(context.Background(), id, "ru-RU")
	if err != nil {
		t.Fatalf("Compose: unexpected error %v", err)
	}
	if got.ID != id {
		t.Errorf("Compose ID = %v, want %v", got.ID, id)
	}
	if got.Type() != domain.MediaTypeSeries {
		t.Errorf("Compose Type = %v, want series", got.Type())
	}
}
