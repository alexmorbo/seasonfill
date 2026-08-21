package qbit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Two instances on one qBittorrent, different categories: one
// connection, one login, and each view still gets its own server-side
// filtered torrent list.
func TestClientCache_SharesConnectionAcrossCategories(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("admin", "secret")
	defer f.close()
	f.torrents = []map[string]any{
		{"hash": "AAA", "name": "S01", "category": "tv-sonarr", "state": "uploading", "added_on": int64(1700000000)},
		{"hash": "BBB", "name": "Movie", "category": "radarr", "state": "uploading", "added_on": int64(1700000100)},
	}

	cc := NewClientCache()
	base := Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second}

	sonarrCfg, radarrCfg := base, base
	sonarrCfg.Category = "tv-sonarr"
	radarrCfg.Category = "radarr"

	tv, err := cc.Get(sonarrCfg)
	if err != nil {
		t.Fatalf("get sonarr view: %v", err)
	}
	movies, err := cc.Get(radarrCfg)
	if err != nil {
		t.Fatalf("get radarr view: %v", err)
	}

	if tv.(*cachedClient).conn != movies.(*cachedClient).conn {
		t.Fatal("identical credentials must share one connection")
	}
	if got := cc.Len(); got != 1 {
		t.Fatalf("want 1 cached connection, got %d", got)
	}

	ctx := context.Background()
	if err := tv.Login(ctx); err != nil {
		t.Fatalf("sonarr login: %v", err)
	}
	if err := movies.Login(ctx); err != nil {
		t.Fatalf("radarr login: %v", err)
	}
	if got := f.loginCalls.Load(); got != 1 {
		t.Fatalf("want exactly one upstream login per connection, got %d", got)
	}

	// Close is a release: the shared session must stay usable afterwards.
	if err := tv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tvList, err := movies.ListTorrents(ctx) // radarr view first, on purpose
	if err != nil {
		t.Fatalf("radarr list: %v", err)
	}
	if len(tvList) != 1 || tvList[0].Hash != "BBB" {
		t.Fatalf("radarr view lost its category filter: %+v", tvList)
	}
	sonarrList, err := tv.ListTorrents(ctx)
	if err != nil {
		t.Fatalf("sonarr list after Close: %v", err)
	}
	if len(sonarrList) != 1 || sonarrList[0].Hash != "AAA" {
		t.Fatalf("sonarr view lost its category filter or its session: %+v", sonarrList)
	}
}

func TestClientCache_DifferentCredentialsDifferentConnections(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("admin", "secret")
	defer f.close()

	cc := NewClientCache()
	first, err := cc.Get(Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Password rotation → new connKey → new connection, old entry stays
	// until it ages out of the cap (documented eviction policy).
	second, err := cc.Get(Config{URL: f.srv.URL, Username: "admin", Password: "rotated", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.(*cachedClient).conn == second.(*cachedClient).conn {
		t.Fatal("different credentials must not share a connection")
	}
	if got := cc.Len(); got != 2 {
		t.Fatalf("want 2 cached connections, got %d", got)
	}
}

func TestClientCache_ConfigErrorBubbles(t *testing.T) {
	t.Parallel()
	cc := NewClientCache()
	if _, err := cc.Get(Config{URL: ""}); err == nil {
		t.Fatal("want ErrInvalidConfig for an empty URL")
	}
	if got := cc.Len(); got != 0 {
		t.Fatalf("a failed build must not cache an entry, got %d", got)
	}
}

// Concurrency guard for `go test -race`: N goroutines racing Get+Login on
// one connection must still produce exactly one upstream login.
func TestClientCache_ConcurrentGetLoginRace(t *testing.T) {
	t.Parallel()
	f := newFakeQbit("admin", "secret")
	defer f.close()

	cc := NewClientCache()
	cfg := Config{URL: f.srv.URL, Username: "admin", Password: "secret", Timeout: 2 * time.Second}

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := cc.Get(cfg)
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = c.Close() }()
			errs[i] = c.Login(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := f.loginCalls.Load(); got != 1 {
		t.Fatalf("want exactly one upstream login under concurrency, got %d", got)
	}
	if got := cc.Len(); got != 1 {
		t.Fatalf("want 1 cached connection, got %d", got)
	}
}
