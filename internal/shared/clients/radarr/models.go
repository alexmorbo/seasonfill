package radarr

import ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"

type movieLookupDTO struct {
	Title        string     `json:"title"`
	TitleSlug    string     `json:"titleSlug"`
	Year         int        `json:"year"`
	TMDBID       int        `json:"tmdbId"`
	IMDBID       string     `json:"imdbId"`
	Overview     string     `json:"overview"`
	RemotePoster string     `json:"remotePoster"`
	Images       []imageDTO `json:"images"`
}

type imageDTO struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl"`
}

type movieDTO struct {
	ID                  int         `json:"id"`
	Title               string      `json:"title"`
	TitleSlug           string      `json:"titleSlug"`
	Year                int         `json:"year"`
	TMDBID              int         `json:"tmdbId"`
	IMDBID              string      `json:"imdbId"`
	Monitored           bool        `json:"monitored"`
	HasFile             bool        `json:"hasFile"`
	MinimumAvailability string      `json:"minimumAvailability"`
	SizeOnDisk          int64       `json:"sizeOnDisk"`
	Statistics          *movieStats `json:"statistics"`
	// MovieFile is Radarr's inline movieFile object, present on GET
	// /api/v3/movie rows when hasFile=true. Unlike Sonarr (many files per
	// series → separate /episodeFile call), a movie has at most one file, so
	// the list response carries it and no extra API call is needed.
	MovieFile *radarrMovieFileDTO `json:"movieFile,omitempty"`
}

type movieStats struct {
	MovieFileCount int   `json:"movieFileCount"`
	SizeOnDisk     int64 `json:"sizeOnDisk"`
}

// radarrMovieFileDTO mirrors the Servarr-family movieFile shape — same nesting
// as sonarr.episodeFileDTO (quality.quality.{name,resolution}, mediaInfo).
type radarrMovieFileDTO struct {
	Quality   radarrQualityRefDTO `json:"quality"`
	MediaInfo *radarrMediaInfoDTO `json:"mediaInfo,omitempty"`
}

type radarrQualityRefDTO struct {
	Quality radarrQualityNestedDTO `json:"quality"`
}

// radarrQualityNestedDTO carries the release quality. Unlike Sonarr's
// qualityNested, Radarr's quality object also exposes resolution (1080, 2160).
type radarrQualityNestedDTO struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Resolution int    `json:"resolution"`
}

// radarrMediaInfoDTO is the mediaInfo subset we read. Absent when Radarr never
// probed the file.
type radarrMediaInfoDTO struct {
	VideoCodec string `json:"videoCodec,omitempty"`
	AudioCodec string `json:"audioCodec,omitempty"`
}

type addMovieRequest struct {
	TMDBID              int                `json:"tmdbId"`
	Title               string             `json:"title"`
	TitleSlug           string             `json:"titleSlug"`
	Year                int                `json:"year"`
	QualityProfileID    int                `json:"qualityProfileId"`
	RootFolderPath      string             `json:"rootFolderPath"`
	Monitored           bool               `json:"monitored"`
	MinimumAvailability string             `json:"minimumAvailability"`
	AddOptions          addMovieAddOptions `json:"addOptions"`
	Tags                []int              `json:"tags,omitempty"`
	Images              []imageDTO         `json:"images,omitempty"`
}

type addMovieAddOptions struct {
	SearchForMovie bool `json:"searchForMovie"`
}

type addMovieResponseDTO struct {
	ID int `json:"id"`
}

func movieDTOToPort(d movieDTO) ports.RadarrMovie {
	m := ports.RadarrMovie{
		RadarrMovieID: d.ID, Title: d.Title, TitleSlug: d.TitleSlug,
		Year: d.Year, TMDBID: d.TMDBID, IMDBID: d.IMDBID,
		Monitored: d.Monitored, HasFile: d.HasFile,
		MinimumAvailability: d.MinimumAvailability, SizeOnDiskBytes: d.SizeOnDisk,
	}
	if d.Statistics != nil && d.Statistics.SizeOnDisk > d.SizeOnDisk {
		m.SizeOnDiskBytes = d.Statistics.SizeOnDisk
	}
	if d.HasFile && d.MovieFile != nil {
		if n := d.MovieFile.Quality.Quality.Name; n != "" {
			m.Quality = &n
		}
		if r := d.MovieFile.Quality.Quality.Resolution; r > 0 {
			m.Resolution = &r
		}
		if mi := d.MovieFile.MediaInfo; mi != nil {
			if v := mi.VideoCodec; v != "" {
				m.VideoCodec = &v
			}
			if a := mi.AudioCodec; a != "" {
				m.AudioCodec = &a
			}
		}
	}
	return m
}
