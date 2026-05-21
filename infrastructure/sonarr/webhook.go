package sonarr

import "time"

// webhookPayloadDTO mirrors Sonarr v4's WebhookPayload + the union of
// its specialised sub-payloads (Grab, Import, ManualInteractionRequired).
// Never imported by domain/ or application/.
type webhookPayloadDTO struct {
	EventType      string     `json:"eventType"`
	InstanceName   string     `json:"instanceName"`
	ApplicationURL string     `json:"applicationUrl"`
	EventTimestamp *time.Time `json:"eventTimestamp,omitempty"`

	DownloadID         string `json:"downloadId,omitempty"`
	DownloadClient     string `json:"downloadClient,omitempty"`
	DownloadClientType string `json:"downloadClientType,omitempty"`

	Release     *webhookReleaseDTO     `json:"release,omitempty"`     // Grab + ManualInteractionRequired
	Series      *webhookSeriesDTO      `json:"series,omitempty"`      // all except Test/Health/AppUpdate
	Episodes    []webhookEpisodeDTO    `json:"episodes,omitempty"`    // Grab/Download/ManualInt/Rename/EpDelete
	EpisodeFile *webhookEpisodeFileDTO `json:"episodeFile,omitempty"` // Download (import success) + Rename
	IsUpgrade   bool                   `json:"isUpgrade,omitempty"`   // Download

	// DownloadStatus + DownloadStatusMessages — ManualInteractionRequired.
	DownloadStatus         string                    `json:"downloadStatus,omitempty"`
	DownloadStatusMessages []webhookStatusMessageDTO `json:"downloadStatusMessages,omitempty"`
}

// webhookReleaseDTO mirrors Sonarr's WebhookRelease.
type webhookReleaseDTO struct {
	Quality           string   `json:"quality,omitempty"`
	QualityVersion    int      `json:"qualityVersion,omitempty"`
	ReleaseGroup      string   `json:"releaseGroup,omitempty"`
	ReleaseTitle      string   `json:"releaseTitle,omitempty"`
	Indexer           string   `json:"indexer,omitempty"`
	Size              int64    `json:"size,omitempty"`
	CustomFormatScore int      `json:"customFormatScore,omitempty"`
	CustomFormats     []string `json:"customFormats,omitempty"`
}

// webhookSeriesDTO mirrors Sonarr's WebhookSeries (subset).
type webhookSeriesDTO struct {
	ID        int    `json:"id"`
	Title     string `json:"title,omitempty"`
	TitleSlug string `json:"titleSlug,omitempty"`
	TvdbID    int    `json:"tvdbId,omitempty"`
	TvMazeID  int    `json:"tvMazeId,omitempty"`
	ImdbID    string `json:"imdbId,omitempty"`
	Type      string `json:"type,omitempty"`
}

// webhookEpisodeDTO mirrors Sonarr's WebhookEpisode (subset).
type webhookEpisodeDTO struct {
	ID            int    `json:"id"`
	EpisodeNumber int    `json:"episodeNumber"`
	SeasonNumber  int    `json:"seasonNumber"`
	Title         string `json:"title,omitempty"`
	SeriesID      int    `json:"seriesId,omitempty"`
	TvdbID        int    `json:"tvdbId,omitempty"`
}

// webhookEpisodeFileDTO mirrors Sonarr's WebhookEpisodeFile (subset).
type webhookEpisodeFileDTO struct {
	ID           int    `json:"id"`
	RelativePath string `json:"relativePath,omitempty"`
	Path         string `json:"path,omitempty"`
	Quality      string `json:"quality,omitempty"`
	ReleaseGroup string `json:"releaseGroup,omitempty"`
	SceneName    string `json:"sceneName,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

// webhookStatusMessageDTO mirrors Sonarr's TrackedDownloadStatusMessage.
type webhookStatusMessageDTO struct {
	Title    string   `json:"title,omitempty"`
	Messages []string `json:"messages,omitempty"`
}
