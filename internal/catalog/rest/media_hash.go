package rest

import (
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
)

// mediaHashForPosterAsset derives the content-addressed media hash for
// the w342 hero poster from the raw canon poster_asset path. Returns
// nil when the path is nil or empty so the wire poster_hash field is
// omitted (FE falls back to the monogram placeholder).
//
// The hash is the same value the prewarm pipeline would compute for
// the synthetic CDN URL (sha256 over
// "https://image.tmdb.org/t/p/w342" + path). Computing it handler-side
// from the canon path removes the dependency on media_assets having
// caught up — tiles render the moment the canon row carries a path,
// and the media handler's on-demand fetch covers "hash known, bytes
// not yet downloaded".
//
// Story 444 (A-1-18) — duplicate of the same helper in
// interface/http/handlers/media_hash.go. Two-line copy avoids the
// import cycle that would otherwise arise from catalog/rest →
// handlers (for the exported alias) and handlers → catalog/rest (for
// InstanceRegistry — see qbit_discover.go).
func mediaHashForPosterAsset(posterAsset *string) *string {
	if posterAsset == nil {
		return nil
	}
	url := appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, *posterAsset)
	if url == "" {
		return nil
	}
	hash := appmedia.HashFromURL(url)
	return &hash
}
