package movie

import "testing"

// TestCollectionCanon_ZeroValue documents the zero value the enrichment mapper
// returns for a nil response (no panic, empty name).
func TestCollectionCanon_ZeroValue(t *testing.T) {
	var c CollectionCanon
	if c.TMDBCollectionID != 0 || c.Name != "" || c.Overview != nil {
		t.Fatalf("unexpected non-zero CollectionCanon: %+v", c)
	}
	if c.Monitored || c.RadarrMonitored {
		t.Fatalf("flags must default false: %+v", c)
	}
}
