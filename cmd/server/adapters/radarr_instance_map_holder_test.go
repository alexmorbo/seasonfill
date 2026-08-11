package adapters

import (
	"sync"
	"testing"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func riFor(name string) scan.RadarrInstance {
	return scan.RadarrInstance{Config: runtime.InstanceSnapshot{Name: name, Type: "radarr"}}
}

func TestRadarrInstanceMapHolder_LoadReturnsDefensiveCopy(t *testing.T) {
	h := NewRadarrInstanceMapHolder(map[string]scan.RadarrInstance{"a": riFor("a")})

	got := h.Load()
	got["a"] = riFor("mutated")
	got["b"] = riFor("b")

	fresh := h.Load()
	if _, ok := fresh["b"]; ok {
		t.Fatalf("mutating the loaded map leaked into the holder")
	}
	if fresh["a"].Config.Name != "a" {
		t.Fatalf("mutating the loaded value leaked into the holder: %q", fresh["a"].Config.Name)
	}
}

func TestRadarrInstanceMapHolder_SeedIsDefensiveCopy(t *testing.T) {
	initial := map[string]scan.RadarrInstance{"a": riFor("a")}
	h := NewRadarrInstanceMapHolder(initial)
	initial["b"] = riFor("b") // must not affect the holder

	if _, ok := h.Load()["b"]; ok {
		t.Fatalf("holder observed a post-seed mutation of the initial map")
	}
}

func TestRadarrInstanceMapHolder_Replace(t *testing.T) {
	h := NewRadarrInstanceMapHolder(nil)
	if len(h.Load()) != 0 {
		t.Fatalf("nil-seeded holder should Load empty")
	}
	h.Replace(map[string]scan.RadarrInstance{"x": riFor("x")})
	got := h.Load()
	if _, ok := got["x"]; !ok || len(got) != 1 {
		t.Fatalf("Replace not observed by Load: %v", got)
	}
}

func TestRadarrInstanceMapHolder_ConcurrentReplaceLoad(t *testing.T) {
	h := NewRadarrInstanceMapHolder(nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); h.Replace(map[string]scan.RadarrInstance{"x": riFor("x")}) }()
		go func() { defer wg.Done(); _ = h.Load() }()
	}
	wg.Wait()
}
