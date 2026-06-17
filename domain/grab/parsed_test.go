package grab_test

import (
	"testing"

	"github.com/alexmorbo/seasonfill/domain/grab"
)

func TestParsed_IsZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    grab.Parsed
		want bool
	}{
		{"zero", grab.Parsed{}, true},
		{"codec", grab.Parsed{Codec: "HEVC"}, false},
		{"source", grab.Parsed{Source: "WEB-DL"}, false},
		{"quality", grab.Parsed{Quality: "WEBDL-2160p"}, false},
		{"resolution", grab.Parsed{Resolution: 2160}, false},
		{"hdr", grab.Parsed{HDRFlags: []string{"HDR10"}}, false},
		{"dub", grab.Parsed{Dub: "MVO"}, false},
		{"languages", grab.Parsed{Languages: []string{"Russian"}}, false},
		{"subs", grab.Parsed{Subs: []string{"RUS"}}, false},
		{"release group", grab.Parsed{ReleaseGroup: "NTb"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.p.IsZero(); got != tt.want {
				t.Fatalf("IsZero()=%v want %v", got, tt.want)
			}
		})
	}
}
