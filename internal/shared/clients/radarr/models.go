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
}

type movieStats struct {
	MovieFileCount int   `json:"movieFileCount"`
	SizeOnDisk     int64 `json:"sizeOnDisk"`
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
	return m
}
