package sonarr

import "time"

type systemStatusDTO struct {
	Version     string `json:"version"`
	InstanceURL string `json:"instanceName"`
}

type seriesDTO struct {
	ID             int            `json:"id"`
	Title          string         `json:"title"`
	TitleSlug      string         `json:"titleSlug"`
	Year           int            `json:"year"`
	SeriesType     string         `json:"seriesType"`
	Monitored      bool           `json:"monitored"`
	QualityProfile int            `json:"qualityProfileId"`
	Tags           []int          `json:"tags"`
	Seasons        []seasonDTO    `json:"seasons"`
	Statistics     *statisticsDTO `json:"statistics,omitempty"`
	TVDBID         int            `json:"tvdbId,omitempty"`
	IMDBID         string         `json:"imdbId,omitempty"`
	TMDBID         int            `json:"tmdbId,omitempty"`
	Status         string         `json:"status,omitempty"`
	Network        string         `json:"network,omitempty"`
	Genres         []string       `json:"genres,omitempty"`
	Runtime        int            `json:"runtime,omitempty"`
	Overview       string         `json:"overview,omitempty"`
	Images         []imageDTO     `json:"images,omitempty"`
	// PreviousAiring is the datetime of the most recently aired
	// episode (Sonarr `previousAiring`). Pointer — Sonarr omits the
	// field for upcoming series with no aired episodes yet.
	PreviousAiring *time.Time `json:"previousAiring,omitempty"`
}

// imageDTO is one entry in Sonarr series.images[]. URL is either a
// relative `/MediaCover/...` path or a fully-qualified URL depending on
// the Sonarr install — pass through verbatim; the UI prefixes when
// stored value starts with `/`.
type imageDTO struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl,omitempty"`
}

type seasonDTO struct {
	SeasonNumber int            `json:"seasonNumber"`
	Monitored    bool           `json:"monitored"`
	Statistics   *statisticsDTO `json:"statistics,omitempty"`
}

// statisticsDTO mirrors Sonarr's nested statistics block. Pointer
// captures absence cleanly — Sonarr omits this for empty series.
//
// 046a adds TotalEpisodeCount + AiredEpisodeCount so the evaluator can
// snapshot the partial-pack counter triplet onto every Decision row.
// Sonarr v3 has emitted these fields since forever; older fixtures that
// only set episodeCount still decode cleanly (zero defaults).
type statisticsDTO struct {
	EpisodeCount      int `json:"episodeCount"`
	EpisodeFileCount  int `json:"episodeFileCount"`
	TotalEpisodeCount int `json:"totalEpisodeCount"`
	AiredEpisodeCount int `json:"airedEpisodeCount"`
}

type episodeDTO struct {
	ID            int       `json:"id"`
	EpisodeNumber int       `json:"episodeNumber"`
	SeasonNumber  int       `json:"seasonNumber"`
	Title         string    `json:"title"`
	Monitored     bool      `json:"monitored"`
	HasFile       bool      `json:"hasFile"`
	AirDateUtc    time.Time `json:"airDateUtc"`
	EpisodeFileID int       `json:"episodeFileId"`
}

type episodeFileDTO struct {
	ID           int        `json:"id"`
	SeriesID     int        `json:"seriesId"`
	SeasonNumber int        `json:"seasonNumber"`
	RelativePath string     `json:"relativePath"`
	Size         int64      `json:"size"`
	Quality      qualityRef `json:"quality"`
}

type qualityRef struct {
	Quality qualityNested `json:"quality"`
}

type qualityNested struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type releaseDTO struct {
	GUID                 string     `json:"guid"`
	Title                string     `json:"title"`
	IndexerID            int        `json:"indexerId"`
	Indexer              string     `json:"indexer"`
	Protocol             string     `json:"protocol"`
	Quality              qualityRef `json:"quality"`
	CustomFormatScore    int        `json:"customFormatScore"`
	Seeders              int        `json:"seeders"`
	Leechers             int        `json:"leechers"`
	Size                 int64      `json:"size"`
	MappedSeasonNumber   int        `json:"mappedSeasonNumber"`
	MappedEpisodeNumbers []int      `json:"mappedEpisodeNumbers"`
	Rejections           []string   `json:"rejections"`
	PublishDate          time.Time  `json:"publishDate"`
	FullSeason           bool       `json:"fullSeason"`
}

type qualityProfileDTO struct {
	ID    int                  `json:"id"`
	Name  string               `json:"name"`
	Items []qualityProfileItem `json:"items"`
}

type qualityProfileItem struct {
	Allowed bool                 `json:"allowed"`
	Quality *qualityNested       `json:"quality,omitempty"`
	Items   []qualityProfileItem `json:"items,omitempty"`
	Name    string               `json:"name,omitempty"`
	ID      int                  `json:"id,omitempty"`
}

type indexerDTO struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Priority int    `json:"priority"`
}

type tagDTO struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type historyResponse struct {
	Records []historyRecord `json:"records"`
}

type historyRecord struct {
	EventType string                 `json:"eventType"`
	Indexer   string                 `json:"indexer,omitempty"`
	Episode   *episodeDTO            `json:"episode,omitempty"`
	Data      map[string]interface{} `json:"data"`
}

type forceGrabRequest struct {
	GUID      string `json:"guid"`
	IndexerID int    `json:"indexerId"`
}

// releaseCreateResponse maps the subset of Sonarr's POST
// /api/v3/release response we read. `downloadClientId` is nullable +
// JsonIgnoreWhenDefault on the server, so the wire form is "absent",
// "null", or an integer. *int decodes all three; nil OR zero coerces
// to empty string at the callsite. See 008b research note for source.
type releaseCreateResponse struct {
	DownloadClientID *int `json:"downloadClientId,omitempty"`
}

// parseResourceDTO mirrors Sonarr v3 ParseResource. We only carry the
// fields B2 needs — title, parsedEpisodeInfo, quality+source+resolution,
// languages, releaseGroup. customFormats and rejections are deliberately
// ignored (the regex pass in parse_extras.go covers them).
type parseResourceDTO struct {
	Title             string                `json:"title"`
	ParsedEpisodeInfo *parsedEpisodeInfoDTO `json:"parsedEpisodeInfo,omitempty"`
}

type parsedEpisodeInfoDTO struct {
	ReleaseTitle   string             `json:"releaseTitle"`
	SeriesTitle    string             `json:"seriesTitle,omitempty"`
	SeasonNumber   int                `json:"seasonNumber"`
	EpisodeNumbers []int              `json:"episodeNumbers,omitempty"`
	ReleaseGroup   string             `json:"releaseGroup,omitempty"`
	Quality        *parseQualityDTO   `json:"quality,omitempty"`
	Languages      []parseLanguageDTO `json:"languages,omitempty"`
}

type parseQualityDTO struct {
	Quality *parseQualityInner `json:"quality,omitempty"`
}

type parseQualityInner struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source,omitempty"`
	Resolution int    `json:"resolution,omitempty"`
}

type parseLanguageDTO struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
