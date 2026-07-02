// Package seriesdetail — see ports.go header.
//
// next_episode_adapter.go (B1b-1). Concrete NextEpisodePort impl consumed
// by SkeletonComposer.buildHero (skeleton.go). It reuses the canon
// episode + episode-text repos already wired for the season/detail
// pipeline — no new SQL. Selection mirrors the (now-deleted) fat
// composer pickNextEpisode: the earliest future-aired non-Specials
// episode, tie-broken by (air_date, season, episode) ASC.
package seriesdetail

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// NextEpisodeAdapter satisfies NextEpisodePort using canon episode rows.
type NextEpisodeAdapter struct {
	episodes     EpisodesPort
	episodeTexts EpisodeTextsPort
	now          func() time.Time
}

// NewNextEpisodeAdapter constructs the adapter. now defaults to
// time.Now().UTC when nil (test seam).
func NewNextEpisodeAdapter(episodes EpisodesPort, episodeTexts EpisodeTextsPort, now func() time.Time) *NextEpisodeAdapter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &NextEpisodeAdapter{episodes: episodes, episodeTexts: episodeTexts, now: now}
}

// NextAired returns the soonest future-aired canon episode. ok=false when
// the series has no future-dated non-Specials episode. A repo error on the
// episode list is returned; a title-lookup miss is non-fatal (empty title).
func (a *NextEpisodeAdapter) NextAired(ctx context.Context, seriesID domain.SeriesID, language string) (NextEpisodeRef, bool, error) {
	eps, err := a.episodes.ListBySeries(ctx, seriesID)
	if err != nil {
		return NextEpisodeRef{}, false, fmt.Errorf("next_episode: list episodes: %w", err)
	}
	now := a.now()
	var best *NextEpisodeRef
	var bestID int64
	for i := range eps {
		e := eps[i]
		if e.SeasonNumber <= 0 { // skip Specials (S0) — TBA by nature
			continue
		}
		if e.AirDate == nil || !e.AirDate.After(now) {
			continue
		}
		cand := NextEpisodeRef{
			SeasonNumber:  e.SeasonNumber,
			EpisodeNumber: e.EpisodeNumber,
			AirDate:       *e.AirDate,
		}
		if best == nil || earlierRef(cand, *best) {
			c := cand
			best = &c
			bestID = e.ID
		}
	}
	if best == nil {
		return NextEpisodeRef{}, false, nil
	}
	// Best-effort localized title (nil-OK port; ErrNotFound → empty title).
	if a.episodeTexts != nil {
		if t, terr := a.episodeTexts.GetWithFallback(ctx, domain.EpisodeID(bestID), language); terr == nil {
			if t.Title != nil {
				best.Title = *t.Title
			}
		}
	}
	return *best, true, nil
}

// earlierRef — (air_date, season, episode) ASC comparator.
func earlierRef(a, b NextEpisodeRef) bool {
	if a.AirDate.Before(b.AirDate) {
		return true
	}
	if a.AirDate.After(b.AirDate) {
		return false
	}
	if a.SeasonNumber != b.SeasonNumber {
		return a.SeasonNumber < b.SeasonNumber
	}
	return a.EpisodeNumber < b.EpisodeNumber
}
