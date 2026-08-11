package radarr

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// webhookPayloadDTO mirrors Radarr v3's WebhookPayload + the union of its
// specialised sub-payloads (Grab, Download, MovieAdded, MovieDelete,
// MovieFileDelete, ManualInteractionRequired). Never imported by domain/ or
// application/. Mirror of sonarr.webhookPayloadDTO.
type webhookPayloadDTO struct {
	EventType      string              `json:"eventType"`
	InstanceName   domain.InstanceName `json:"instanceName"`
	ApplicationURL string              `json:"applicationUrl"`
	EventTimestamp *time.Time          `json:"eventTimestamp,omitempty"`

	DownloadID string `json:"downloadId,omitempty"`

	Movie     *webhookMovieDTO     `json:"movie,omitempty"`
	MovieFile *webhookMovieFileDTO `json:"movieFile,omitempty"` // Download (import success)
	Release   *webhookReleaseDTO2  `json:"release,omitempty"`   // Grab
	IsUpgrade bool                 `json:"isUpgrade,omitempty"`

	// MovieFileDelete carries deletedFiles; MovieDelete carries deletedFiles +
	// the movie block. Captured for parity, not consumed beyond routing.
	DeletedFiles bool `json:"deletedFiles,omitempty"`

	DownloadStatusMessages []webhookStatusMessageDTO2 `json:"downloadStatusMessages,omitempty"`
}

// webhookMovieDTO mirrors Radarr's WebhookMovie (subset).
type webhookMovieDTO struct {
	ID                  int        `json:"id"`
	Title               string     `json:"title,omitempty"`
	TitleSlug           string     `json:"titleSlug,omitempty"`
	Year                int        `json:"year,omitempty"`
	TMDBID              int        `json:"tmdbId,omitempty"`
	IMDBID              string     `json:"imdbId,omitempty"`
	FolderPath          string     `json:"folderPath,omitempty"`
	Monitored           *bool      `json:"monitored,omitempty"`
	MinimumAvailability string     `json:"minimumAvailability,omitempty"`
	ReleaseDate         *time.Time `json:"physicalRelease,omitempty"`
}

type webhookMovieFileDTO struct {
	ID           int    `json:"id"`
	RelativePath string `json:"relativePath,omitempty"`
	Path         string `json:"path,omitempty"`
	Quality      string `json:"quality,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type webhookReleaseDTO2 struct {
	ReleaseTitle string `json:"releaseTitle,omitempty"`
	Indexer      string `json:"indexer,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type webhookStatusMessageDTO2 struct {
	Title    string   `json:"title,omitempty"`
	Messages []string `json:"messages,omitempty"`
}
