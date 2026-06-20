package repositories

// Test helpers that span multiple repositories' tests. They moved here
// from grab_repository_test.go + grab_list_test.go during story 431
// (A-1-5) when the grab repository graduated to internal/grab/persistence.
//
// Story 443 (A-1-17) moved counter_repository_test (the last consumer
// of seedGrab in this package) into internal/catalog/persistence, so
// only the ptr* shorthands remain — sample_helpers_test still calls
// them from sampleCanon. seedGrab now lives alongside the catalog
// tests that need it (internal/catalog/persistence/sample_helpers_test.go).
//
// Future story (D-0+ or 449 model split) will relocate the ptr*
// helpers into internal/shared/testhelpers; until then they live
// alongside the legacy stays that depend on them (videos_repository_test
// + sample_helpers_test).

import (
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ptrTMDBID / ptrIMDBID / ptrTVDBID are thin pointer-to-typed-ID
// shorthands consumed by sample_helpers_test.go's sampleCanon (the
// remaining stays use sampleCanon as their seed fixture).
func ptrTMDBID(i int) *domain.TMDBID {
	v := domain.TMDBID(i)
	return &v
}

func ptrIMDBID(s string) *domain.IMDBID {
	v := domain.IMDBID(s)
	return &v
}

func ptrTVDBID(i int) *domain.TVDBID {
	v := domain.TVDBID(i)
	return &v
}
