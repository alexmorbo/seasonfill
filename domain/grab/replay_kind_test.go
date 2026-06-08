package grab_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/domain/grab"
)

func TestDeriveReplayKind(t *testing.T) {
	t.Parallel()
	parentID := uuid.New()

	cases := []struct {
		name   string
		cur    grab.Record
		parent *grab.Record
		want   grab.ReplayKind
	}{
		{
			name: "no replay_of_id is primary",
			cur:  grab.Record{},
			want: grab.ReplayKindPrimary,
		},
		{
			name: "primary even with parent pointer wins on nil ReplayOfID",
			cur:  grab.Record{},
			parent: &grab.Record{
				Parsed: &grab.Parsed{Resolution: 1080},
			},
			want: grab.ReplayKindPrimary,
		},
		{
			name:   "missing parent on a replay is other",
			cur:    grab.Record{ReplayOfID: &parentID},
			parent: nil,
			want:   grab.ReplayKindOther,
		},
		{
			name: "1080p to 2160p is quality",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 2160},
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 1080}},
			want:   grab.ReplayKindQuality,
		},
		{
			name: "fallback Quality string drives quality when resolution unset",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Quality:    "WEBDL-2160p",
			},
			parent: &grab.Record{Quality: "WEBDL-1080p"},
			want:   grab.ReplayKindQuality,
		},
		{
			name: "HDR-only difference at same resolution is quality",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 2160, HDRFlags: []string{"HDR10"}},
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 2160}},
			want:   grab.ReplayKindQuality,
		},
		{
			name: "dub gained on top of equal resolution is dub",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 1080, Dub: "MVO"},
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 1080}},
			want:   grab.ReplayKindDub,
		},
		{
			name: "both sides dubbed without quality bump is other",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 1080, Dub: "DUB"},
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 1080, Dub: "MVO"}},
			want:   grab.ReplayKindOther,
		},
		{
			name: "quality regression falls through to other",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 720},
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 1080}},
			want:   grab.ReplayKindOther,
		},
		{
			name: "quality wins over dub when both axes change",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 2160, Dub: "MVO"},
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 1080}},
			want:   grab.ReplayKindQuality,
		},
		{
			name: "parent with no Parsed at all stays other on dub axis",
			cur: grab.Record{
				ReplayOfID: &parentID,
				Parsed:     &grab.Parsed{Resolution: 1080, Dub: "MVO"},
			},
			parent: &grab.Record{},
			want:   grab.ReplayKindOther,
		},
		{
			name: "current with no Parsed and parent at 1080p falls to other (no rank, no dub)",
			cur: grab.Record{
				ReplayOfID: &parentID,
			},
			parent: &grab.Record{Parsed: &grab.Parsed{Resolution: 1080}},
			want:   grab.ReplayKindOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grab.DeriveReplayKind(tc.cur, tc.parent)
			assert.Equal(t, tc.want, got)
		})
	}
}
