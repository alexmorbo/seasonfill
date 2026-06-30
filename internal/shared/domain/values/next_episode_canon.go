package values

import (
	"encoding/json"
	"fmt"
	"time"
)

// NextEpisodeCanon is the TMDB-canon "next episode" hero block. Season
// and episode numbers travel as ints (they are 1-based wire numbers,
// not internal IDs — the typed-ID guard intentionally does NOT cover
// them). Title is a localized VO so the renderer carries its language.
//
// DaysUntil is a computed field: floor((airDate - now) / 24h). It is
// NOT recomputed inside the VO — callers compute it once when
// constructing and the value stays stable for the response lifetime.
type NextEpisodeCanon struct {
	seasonNumber  int
	episodeNumber int
	title         Title
	airDate       time.Time
	daysUntil     int
}

func NewNextEpisodeCanon(
	seasonNumber int,
	episodeNumber int,
	title Title,
	airDate time.Time,
	daysUntil int,
) (NextEpisodeCanon, error) {
	if seasonNumber <= 0 || episodeNumber <= 0 {
		return NextEpisodeCanon{}, fmt.Errorf("%w: season=%d episode=%d", ErrNextEpisodeInvalid, seasonNumber, episodeNumber)
	}
	if title.IsZero() {
		return NextEpisodeCanon{}, fmt.Errorf("%w: empty title", ErrNextEpisodeInvalid)
	}
	if airDate.IsZero() {
		return NextEpisodeCanon{}, fmt.Errorf("%w: zero air_date", ErrNextEpisodeInvalid)
	}
	return NextEpisodeCanon{
		seasonNumber:  seasonNumber,
		episodeNumber: episodeNumber,
		title:         title,
		airDate:       airDate,
		daysUntil:     daysUntil,
	}, nil
}

func (n NextEpisodeCanon) SeasonNumber() int  { return n.seasonNumber }
func (n NextEpisodeCanon) EpisodeNumber() int { return n.episodeNumber }
func (n NextEpisodeCanon) Title() Title       { return n.title }
func (n NextEpisodeCanon) AirDate() time.Time { return n.airDate }
func (n NextEpisodeCanon) DaysUntil() int     { return n.daysUntil }
func (n NextEpisodeCanon) IsZero() bool       { return n.seasonNumber == 0 && n.episodeNumber == 0 }
func (n NextEpisodeCanon) Equal(o NextEpisodeCanon) bool {
	return n.seasonNumber == o.seasonNumber &&
		n.episodeNumber == o.episodeNumber &&
		n.title.Equal(o.title) &&
		n.airDate.Equal(o.airDate) &&
		n.daysUntil == o.daysUntil
}
func (n NextEpisodeCanon) String() string {
	return fmt.Sprintf("S%02dE%02d %q", n.seasonNumber, n.episodeNumber, n.title.Value())
}

type nextEpisodeCanonJSON struct {
	SeasonNumber  int       `json:"season_number"`
	EpisodeNumber int       `json:"episode_number"`
	Title         Title     `json:"title"`
	AirDate       time.Time `json:"air_date"`
	DaysUntil     int       `json:"days_until"`
}

func (n NextEpisodeCanon) MarshalJSON() ([]byte, error) {
	if n.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(nextEpisodeCanonJSON{
		SeasonNumber:  n.seasonNumber,
		EpisodeNumber: n.episodeNumber,
		Title:         n.title,
		AirDate:       n.airDate,
		DaysUntil:     n.daysUntil,
	})
}

func (n *NextEpisodeCanon) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*n = NextEpisodeCanon{}
		return nil
	}
	var raw nextEpisodeCanonJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("values: next_episode_canon unmarshal: %w", err)
	}
	parsed, err := NewNextEpisodeCanon(raw.SeasonNumber, raw.EpisodeNumber, raw.Title, raw.AirDate, raw.DaysUntil)
	if err != nil {
		return err
	}
	*n = parsed
	return nil
}
