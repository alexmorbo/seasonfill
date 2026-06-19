package qbit_test

import (
	"testing"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

func TestParseSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		title   string
		want    *int
		wantNil bool
	}{
		{name: "empty", title: "", wantNil: true},
		{name: "whitespace only", title: "   \t  ", wantNil: true},
		{name: "single episode canonical", title: "Show.S03E07.1080p.WEB-DL", want: new(3)},
		{name: "lowercase", title: "show.s03e07.1080p.web-dl", want: new(3)},
		{name: "mixed case", title: "Show.s03E07.1080p", want: new(3)},
		{name: "two episodes same season E-pair", title: "Show.S03E07E08.1080p", want: new(3)},
		{name: "two episodes same season repeating prefix", title: "Show.S03E07.S03E08.1080p", want: new(3)},
		{name: "three episodes same season", title: "Show.S05E01E02E03.PROPER", want: new(5)},
		{name: "multi-season pack returns nil", title: "Show.S02E10.S03E01.MIX.1080p", wantNil: true},
		{name: "full-series pack returns nil", title: "Show.Complete.Series.1080p", wantNil: true},
		{name: "season-only pack returns nil", title: "Show.S03.PACK.1080p", wantNil: true},
		{name: "no episode marker returns nil", title: "Random.Movie.2024.1080p", wantNil: true},
		{name: "dot separator S03.E07", title: "Show.S03.E07.1080p", want: new(3)},
		{name: "1x07 alternate schema returns nil", title: "Show.1x07.1080p", wantNil: true},
		{name: "four-digit season", title: "Show.S1999E01.WEIRD", want: new(1999)},
		{name: "embedded false positive must not match", title: "FANTASY3E07.RANDOM", wantNil: true},
		// PRD-relevant real-world title shapes from the existing
		// torrent corpus on prod (see qbit/sync_test.go fixtures
		// at lines 84 / 94 and infrastructure/sonarr fixtures).
		{name: "Severance S02E03 WEBRip", title: "Severance.S02E03.WEBRip.x264-NTb [RUS Sub]", want: new(2)},
		{name: "Foundation S02 pack returns nil", title: "Foundation.S02.2160p.WEB-DL.HEVC", wantNil: true},
		{name: "Andor S02 DUB returns nil", title: "Andor.S02.1080p.WEB-DL.DDP5.1.H.264 [DUB]", wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := qbit.ParseSeason(tt.title)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("ParseSeason(%q) = %d, want nil", tt.title, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseSeason(%q) = nil, want %d", tt.title, *tt.want)
			}
			if *got != *tt.want {
				t.Fatalf("ParseSeason(%q) = %d, want %d", tt.title, *got, *tt.want)
			}
		})
	}
}
