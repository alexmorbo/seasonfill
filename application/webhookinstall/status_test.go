package webhookinstall

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStatusCache_GetSetDelete(t *testing.T) {
	t.Parallel()
	c := NewStatusCache()
	if _, ok := c.Get("alpha"); ok {
		t.Fatalf("empty cache should miss")
	}
	now := time.Now()
	c.Set("alpha", Status{Installed: true, LastCheckedAt: now})
	got, ok := c.Get("alpha")
	if !ok || !got.Installed || !got.LastCheckedAt.Equal(now) {
		t.Fatalf("unexpected: %+v ok=%v", got, ok)
	}
	c.Delete("alpha")
	if _, ok := c.Get("alpha"); ok {
		t.Fatalf("entry should be gone")
	}
}

func TestStatusCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := NewStatusCache()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); c.Set("alpha", Status{Installed: i%2 == 0, LastCheckedAt: time.Now()}) }(i)
		go func() { defer wg.Done(); _, _ = c.Get("alpha") }()
	}
	wg.Wait()
}

func TestPublicURLFromContext(t *testing.T) {
	t.Parallel()
	if got := PublicURLFromContext(context.Background()); got != "" {
		t.Fatalf("empty context should return empty string, got %q", got)
	}
	ctx := context.WithValue(context.Background(), RequestPublicURLKey{}, "https://sf.example")
	if got := PublicURLFromContext(ctx); got != "https://sf.example" {
		t.Fatalf("expected stashed value, got %q", got)
	}
	// Wrong type stored under key → returns "".
	bad := context.WithValue(context.Background(), RequestPublicURLKey{}, 42)
	if got := PublicURLFromContext(bad); got != "" {
		t.Fatalf("wrong-type value should yield empty, got %q", got)
	}
}
