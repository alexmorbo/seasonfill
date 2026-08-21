package torrentsync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func mkInfo(hash, name string, group qbit.StateGroup) qbit.TorrentInfo {
	return qbit.TorrentInfo{
		Hash:       hash,
		Name:       name,
		StateRaw:   "downloading",
		StateGroup: group,
		Size:       1 << 20,
	}
}

func TestStore_PutGet(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.EnsureInstance("alpha")
	e := Entry{
		Info:       mkInfo("aaaa", "show", qbit.StateGroupDownloading),
		StateGroup: qbit.StateGroupDownloading,
		SyncedAt:   time.Now().UTC(),
	}
	s.Put("alpha", e)

	got, ok := s.Get("alpha", "aaaa")
	require.True(t, ok)
	assert.Equal(t, "show", got.Info.Name)
	assert.Equal(t, 1, s.Len("alpha"))
}

func TestStore_DropInstance(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.EnsureInstance("alpha")
	s.Put("alpha", Entry{Info: mkInfo("aaaa", "x", qbit.StateGroupDownloading)})
	s.DropInstance("alpha")
	_, ok := s.Get("alpha", "aaaa")
	assert.False(t, ok)
	assert.Equal(t, 0, s.Len("alpha"))
}

func TestStore_SeriesMapping(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.EnsureInstance("alpha")
	s.SetSeriesMapping("alpha", "aaaa", 42)
	s.SetSeriesMapping("alpha", "bbbb", 42)
	hashes := s.HashesFor("alpha", 42)
	assert.ElementsMatch(t, []string{"aaaa", "bbbb"}, hashes)
}

func TestStore_DeletePrunesSeriesIndex(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.EnsureInstance("alpha")
	s.SetSeriesMapping("alpha", "aaaa", 42)
	s.Delete("alpha", "aaaa")
	assert.Empty(t, s.HashesFor("alpha", 42))
}

func TestCountersFrom(t *testing.T) {
	t.Parallel()
	info := qbit.TorrentInfo{
		Ratio:        2.5,
		Uploaded:     10_000,
		TimeActive:   2 * time.Hour,
		SeedingTime:  time.Hour,
		Popularity:   1.2,
		LastActivity: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	c := CountersFrom(info)
	assert.Equal(t, 2.5, c.Ratio)
	assert.EqualValues(t, 7200, c.TimeActiveS)
	assert.EqualValues(t, 3600, c.SeedingTimeS)
}

func TestCountersDirty(t *testing.T) {
	t.Parallel()
	a := Counters{Ratio: 1.0, Uploaded: 100, TimeActiveS: 60}
	b := a
	assert.False(t, CountersDirty(a, b))
	b.Uploaded = 200
	assert.True(t, CountersDirty(a, b))
}

func TestStore_MovieIndex_SetLookupAndDelete(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.EnsureInstance("radarr-main")

	s.SetMovieMapping("radarr-main", "aaaa", 42)
	s.SetMovieMapping("radarr-main", "bbbb", 42)
	s.SetMovieMapping("radarr-main", "cccc", 43)

	assert.Equal(t, domain.RadarrMovieID(42), s.MovieForHash("radarr-main", "aaaa"))
	assert.Equal(t, domain.RadarrMovieID(43), s.MovieForHash("radarr-main", "cccc"))
	assert.Equal(t, domain.RadarrMovieID(0), s.MovieForHash("radarr-main", "zzzz"))
	assert.Equal(t, domain.RadarrMovieID(0), s.MovieForHash("other", "aaaa"))
	assert.ElementsMatch(t, []string{"aaaa", "bbbb"}, s.HashesForMovieID("radarr-main", 42))
	assert.Empty(t, s.HashesForMovieID("radarr-main", 99))

	// The series index must not see movie mappings and vice versa.
	assert.Equal(t, domain.SonarrSeriesID(0), s.SeriesForHash("radarr-main", "aaaa"))

	s.Delete("radarr-main", "aaaa")
	assert.Equal(t, domain.RadarrMovieID(0), s.MovieForHash("radarr-main", "aaaa"))
	assert.Equal(t, []string{"bbbb"}, s.HashesForMovieID("radarr-main", 42))

	s.Delete("radarr-main", "cccc")
	assert.Empty(t, s.HashesForMovieID("radarr-main", 43))

	s.DropInstance("radarr-main")
	assert.Equal(t, domain.RadarrMovieID(0), s.MovieForHash("radarr-main", "bbbb"))
}

func TestStore_SetMovieMapping_RejectsZeroIDAndEmptyHash(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.EnsureInstance("radarr-main")
	s.SetMovieMapping("radarr-main", "aaaa", 0)
	s.SetMovieMapping("radarr-main", "", 42)
	assert.Equal(t, domain.RadarrMovieID(0), s.MovieForHash("radarr-main", "aaaa"))
	assert.Empty(t, s.HashesForMovieID("radarr-main", 42))
}
