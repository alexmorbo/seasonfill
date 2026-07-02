// Package rest — seriesdetail HTTP handlers.
//
// series_detail.go retains the shared domain→DTO projections used by the
// surviving per-section handlers: mapSeasons (season endpoint),
// mapRecommendations (recommendations endpoint), sourceStringSlice. The
// fat SeriesDetailHandler + toSeriesDetailResponse were removed at the
// B1b cutover (GET /series/:id now returns SkeletonDTO).
package rest

import (
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// mapSeasons projects the composer's SeasonDetail slice onto the DTO.
// Story 379 refactor: takes *Detail so it can read d.QueueRecords for
// the per-season downloading_count chip. Pure projection; no DB / IO.
func mapSeasons(d *seriesdetail.Detail) []dto.Season {
	if d == nil {
		return []dto.Season{}
	}
	out := make([]dto.Season, 0, len(d.Seasons))
	for _, s := range d.Seasons {
		ds := dto.Season{
			SeasonNumber: s.Canon.SeasonNumber,
			Name:         s.Canon.Name,
			Overview:     s.Canon.Overview,
			AirDate:      s.Canon.AirDate,
			PosterAsset:  s.Canon.PosterAsset,
			EpisodeCount: 0,
			Episodes:     make([]dto.Episode, 0, len(s.Episodes)),
		}
		if s.Canon.EpisodeCount != nil {
			ds.EpisodeCount = *s.Canon.EpisodeCount
		}
		for _, e := range s.Episodes {
			ep := dto.Episode{
				EpisodeNumber:  e.Canon.EpisodeNumber,
				AirDate:        e.Canon.AirDate,
				RuntimeMinutes: e.Canon.RuntimeMinutes,
				FinaleType:     e.Canon.FinaleType,
				StillAsset:     e.Canon.StillAsset,
			}
			if e.Canon.SonarrEpisodeID != nil {
				v := *e.Canon.SonarrEpisodeID
				ep.SonarrEpisodeID = &v
			}
			if e.Text != nil {
				ep.Title = e.Text.Title
				ep.TitleLanguage = e.Text.Language
				ep.Overview = e.Text.Overview
				ep.OverviewLanguage = e.Text.Language
			}
			if e.State != nil {
				ep.Monitored = e.State.Monitored
				ep.HasFile = e.State.HasFile
				ep.Quality = e.State.Quality
				ep.SizeBytes = e.State.SizeBytes
				ep.VideoCodec = e.State.VideoCodec
				ep.AudioCodec = e.State.AudioCodec
				ep.AudioChannels = e.State.AudioChannels
				ep.ReleaseGroup = e.State.ReleaseGroup
			}
			ds.Episodes = append(ds.Episodes, ep)
		}
		// Story 377: prefer the persisted Sonarr season.statistics
		// projection over walking episode_states. episode_states is
		// empty for fully-on-disk seasons skipped by
		// scan_skip_handled_seasons, which is the bug this fixes.
		// EpisodeCount on the dto is the "rendered episodes total" — we
		// prefer Stats.TotalEpisodeCount (includes unaired episodes) so
		// the accordion header "X/Y на диске" matches Sonarr.
		if s.Stats != nil {
			ds.Monitored = s.Stats.Monitored
			ds.OnDiskCount = s.Stats.EpisodeFileCount
			missing := max(s.Stats.AiredEpisodeCount-s.Stats.EpisodeFileCount, 0)
			ds.MissingCount = missing
			if s.Stats.TotalEpisodeCount > 0 {
				ds.EpisodeCount = s.Stats.TotalEpisodeCount
			}
		} else {
			var onDisk, missing int
			for _, e := range s.Episodes {
				if e.State != nil && e.State.HasFile {
					onDisk++
				} else {
					missing++
				}
			}
			ds.OnDiskCount = onDisk
			ds.MissingCount = missing
		}
		if ds.EpisodeCount == 0 {
			ds.EpisodeCount = len(s.Episodes)
		}
		// Story 379: per-season downloading chip. Count Sonarr queue
		// records with status=="downloading" whose seasonNumber matches.
		// 0 when no records OR Sonarr unreachable (degraded[]).
		for _, q := range d.QueueRecords {
			if q.SeasonNumber == s.Canon.SeasonNumber && q.Status == "downloading" {
				ds.DownloadingCount++
			}
		}
		out = append(out, ds)
	}
	return out
}

func mapRecommendations(recs []seriesdetail.RecommendationDetail) []dto.Recommendation {
	out := make([]dto.Recommendation, 0, len(recs))
	for _, r := range recs {
		m := dto.Recommendation{
			SeriesID:       r.Series.ID,
			TMDBSeriesID:   r.Series.TMDBID,
			Title:          r.Series.Title,
			Year:           r.Series.Year,
			PosterAsset:    r.Series.PosterAsset,
			TMDBRating:     r.Series.TMDBRating,
			InLibrary:      r.InLibrary,
			InstanceName:   r.InstanceName,
			SonarrSeriesID: r.SonarrSeriesID,
		}
		out = append(out, m)
	}
	return out
}

// sourceStringSlice projects []enrichment.Source → []string for
// the wire.
func sourceStringSlice(s []enrichment.Source) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		out = append(out, string(v))
	}
	return out
}
