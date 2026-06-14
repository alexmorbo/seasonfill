package enrichment

import (
	"github.com/alexmorbo/seasonfill/application/media"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// composePrewarmAssets builds the pre-warm payload per PRD §6.4.
// Paths come from canon entities (poster_asset / backdrop_asset /
// season.poster_asset / person.profile_asset / network.logo_asset)
// + tv.Videos for the trailer thumbnail. Each non-empty path
// produces one MediaPrewarmRequest with the appropriate size variant.
func composePrewarmAssets(canon series.Canon, m mappedPayload, tv *tmdb.TVResponse) []MediaPrewarmRequest {
	const (
		sizePosterGrid   = "w342"
		sizePosterHero   = "w780"
		sizeBackdropHero = "w1280"
		sizeLogoNetwork  = "w185"
		sizeProfileCast  = "w185"
		sizeSeasonPoster = "w154"
		sizeStillEpisode = "w300"
	)
	reqs := make([]MediaPrewarmRequest, 0, 32)

	push := func(size string, path *string, kind string) {
		if path == nil || *path == "" {
			return
		}
		url := media.BuildTMDBImageURL(size, *path)
		if url == "" {
			return
		}
		reqs = append(reqs, MediaPrewarmRequest{
			UpstreamURL: url,
			Kind:        kind,
			Extension:   media.ExtractExt(*path),
		})
	}

	// Series poster: both grid + hero variants.
	push(sizePosterGrid, canon.PosterAsset, "poster_w342")
	push(sizePosterHero, canon.PosterAsset, "poster_w780")
	push(sizeBackdropHero, canon.BackdropAsset, "backdrop_w1280")

	// Network logos.
	for _, n := range m.Networks {
		push(sizeLogoNetwork, n.LogoAsset, "network_logo_w185")
	}

	// Top-10 cast by stub order in PersonStubs (mapper already
	// preserves credit order).
	for i, p := range m.PersonStubs {
		if i >= 10 {
			break
		}
		push(sizeProfileCast, p.ProfileAsset, "profile_w185")
	}

	// Season posters — every season we mapped.
	for _, s := range m.Seasons {
		push(sizeSeasonPoster, s.PosterAsset, "season_poster_w154")
	}

	// Episode stills — w300 matches what EpisodeRow.tsx renders. Cold-start
	// enrichment now lands these bytes so first expand of the seasons
	// accordion never 404s on a still. Async-only on the composer side
	// (Resolve, not ResolveSync) so a miss renders the monogram — story 322.
	for _, e := range m.Episodes {
		push(sizeStillEpisode, e.StillAsset, "still_w300")
	}

	// Trailer thumbnail (best-effort). Pick first Official Trailer
	// from the YouTube site; the thumbnail URL pattern is
	// img.youtube.com/vi/{key}/hqdefault.jpg.
	if thumb := pickTrailerThumbURL(tv); thumb != "" {
		reqs = append(reqs, MediaPrewarmRequest{
			UpstreamURL: thumb,
			Kind:        "trailer_thumb",
			Extension:   "jpg",
		})
	}
	return reqs
}

// pickTrailerThumbURL returns the YouTube thumbnail URL for the
// best-quality trailer entry, or empty when none is present. Best
// quality = Official=true + Type="Trailer" + Site="YouTube"; first
// matching row wins. The thumbnail is hotlinked from i.ytimg.com —
// the downloader's TMDB HTTP client is shared (img.youtube.com is
// NOT blocked by RU DPI; the shared client still applies the proxy
// when configured, which is harmless).
func pickTrailerThumbURL(tv *tmdb.TVResponse) string {
	if tv == nil || tv.Videos == nil {
		return ""
	}
	for _, v := range tv.Videos.Results {
		if v.Site != "YouTube" || v.Type != "Trailer" || !v.Official {
			continue
		}
		if v.Key == "" {
			continue
		}
		return "https://i.ytimg.com/vi/" + v.Key + "/hqdefault.jpg"
	}
	return ""
}
