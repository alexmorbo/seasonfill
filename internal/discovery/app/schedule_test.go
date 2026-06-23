package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

func TestScheduleFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind disco.Kind
		want time.Duration
	}{
		{disco.KindTrendingDay, 6 * time.Hour},
		{disco.KindTrendingWeek, 24 * time.Hour},
		{disco.KindPopular, 24 * time.Hour},
		{disco.KindByGenre, 24 * time.Hour},
		{disco.KindByNetwork, 24 * time.Hour},
		{disco.KindByKeyword, 7 * 24 * time.Hour},
		{disco.Kind("__unknown__"), 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, app.ScheduleFor(tc.kind))
		})
	}
}

func TestPagesFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind disco.Kind
		want int
	}{
		{disco.KindTrendingDay, 5},
		{disco.KindTrendingWeek, 5},
		{disco.KindPopular, 5},
		{disco.KindByGenre, 3},
		{disco.KindByNetwork, 3},
		{disco.KindByKeyword, 3},
		{disco.Kind("__unknown__"), 1},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, app.PagesFor(tc.kind))
		})
	}
}
