package sonarr

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEpisodeFileDTO_DecodesMediaInfo(t *testing.T) {
	raw := []byte(`{
		"id":12345,
		"seriesId":1,
		"seasonNumber":5,
		"episodeIds":[101],
		"path":"/tv/Show/S05E01.mkv",
		"size":1073741824,
		"releaseGroup":"RARBG",
		"quality":{"quality":{"id":19,"name":"WEBDL-1080p"}},
		"mediaInfo":{"videoCodec":"HEVC","audioCodec":"DDP","audioChannels":5.1}
	}`)
	var d episodeFileDTO
	require.NoError(t, json.Unmarshal(raw, &d))
	require.NotNil(t, d.MediaInfo)
	require.Equal(t, "HEVC", d.MediaInfo.VideoCodec)
	require.Equal(t, "DDP", d.MediaInfo.AudioCodec)
	require.Equal(t, "5.1", string(d.MediaInfo.AudioChannels))

	p := episodeFilePayloadFromDTO(d)
	require.Equal(t, "HEVC", p.VideoCodec)
	require.Equal(t, "DDP", p.AudioCodec)
	require.Equal(t, "5.1", p.AudioChannels)
	require.Equal(t, "RARBG", p.ReleaseGroup)
}

func TestEpisodeFileDTO_NoMediaInfo(t *testing.T) {
	raw := []byte(`{"id":1,"quality":{"quality":{"id":1,"name":"Unknown"}}}`)
	var d episodeFileDTO
	require.NoError(t, json.Unmarshal(raw, &d))
	require.Nil(t, d.MediaInfo)
	p := episodeFilePayloadFromDTO(d)
	require.Empty(t, p.VideoCodec)
	require.Empty(t, p.AudioCodec)
	require.Empty(t, p.AudioChannels)
}
