package sonarr_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

func TestExtractExtras(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		wantCdc  string
		wantHDR  []string
		wantDub  string
		wantSubs []string
	}{
		{
			name:    "empty",
			title:   "",
			wantHDR: []string{}, wantSubs: []string{},
		},
		{
			name:    "canonical RU sample",
			title:   "For All Mankind / S5E1-10 of 10 [2026, HEVC, HDR10, HDR10+, Dolby Vision, WEB-DL 2160p] 4 x + Original + ) RUS",
			wantCdc: "HEVC",
			wantHDR: []string{"HDR10+", "DV"},
			wantDub: "Original", wantSubs: []string{"RUS"},
		},
		{
			name:    "plain x264 WEBRip RUS sub",
			title:   "Severance.S02E03.WEBRip.x264-NTb [RUS Sub]",
			wantCdc: "H.264",
			wantHDR: []string{}, wantSubs: []string{"RUS"},
		},
		{
			name:    "HEVC DV standalone",
			title:   "Foundation.S02.2160p.WEB-DL.HEVC.DV-PaODeQuixoTe",
			wantCdc: "HEVC",
			wantHDR: []string{"DV"}, wantSubs: []string{},
		},
		{
			name:    "MVO marker dominates DUB substring in Multi",
			title:   "Дом дракона / S02 / WEB-DL 1080p [MVO LostFilm]",
			wantDub: "MVO",
			wantHDR: []string{}, wantSubs: []string{},
		},
		{
			name:    "DUB-only",
			title:   "Andor.S02.1080p.WEB-DL.DDP5.1.H.264 [DUB]",
			wantCdc: "H.264",
			wantDub: "DUB",
			wantHDR: []string{}, wantSubs: []string{},
		},
		{
			name:    "Multi audio + ENG subs",
			title:   "Game.Of.Thrones.S08.2160p.HDR10.MultiAudio.WEBRip.x265 [Subs: ENG]",
			wantCdc: "HEVC",
			wantHDR: []string{"HDR10"},
			wantDub: "Multi", wantSubs: []string{"ENG"},
		},
		{
			name:    "AV1 HLG",
			title:   "Tales.From.The.Loop.S01.2160p.HLG.WEB-DL.AV1 [Original]",
			wantCdc: "AV1",
			wantHDR: []string{"HLG"},
			wantDub: "Original", wantSubs: []string{},
		},
		{
			name:    "DVD rip must not match DV",
			title:   "The Wire S01 DVDRip x264 RUS Sub",
			wantCdc: "H.264",
			wantHDR: []string{}, wantSubs: []string{"RUS"},
		},
		{
			name:    "no markers at all",
			title:   "Something.Random.S01E01",
			wantHDR: []string{}, wantSubs: []string{},
		},
		{
			name:    "HDR10+ alone — HDR10 must not also fire",
			title:   "Foundation.S02.2160p.HDR10+.WEB-DL.HEVC",
			wantCdc: "HEVC",
			wantHDR: []string{"HDR10+"}, wantSubs: []string{},
		},
		{
			name:    "Multi subs",
			title:   "Severance.S02.WEB-DL.2160p.HEVC.HDR10 [Multi Subs]",
			wantCdc: "HEVC",
			wantHDR: []string{"HDR10"}, wantSubs: []string{"MULTI"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sonarr.ExtractExtras(tt.title)
			if got.Codec != tt.wantCdc {
				t.Fatalf("Codec=%q want %q", got.Codec, tt.wantCdc)
			}
			sort.Strings(got.HDRFlags)
			want := make([]string, len(tt.wantHDR))
			copy(want, tt.wantHDR)
			sort.Strings(want)
			if !reflect.DeepEqual(got.HDRFlags, want) {
				t.Fatalf("HDRFlags=%v want %v", got.HDRFlags, want)
			}
			if got.Dub != tt.wantDub {
				t.Fatalf("Dub=%q want %q", got.Dub, tt.wantDub)
			}
			sort.Strings(got.Subs)
			wantSubs := make([]string, len(tt.wantSubs))
			copy(wantSubs, tt.wantSubs)
			sort.Strings(wantSubs)
			if !reflect.DeepEqual(got.Subs, wantSubs) {
				t.Fatalf("Subs=%v want %v", got.Subs, wantSubs)
			}
		})
	}
}
