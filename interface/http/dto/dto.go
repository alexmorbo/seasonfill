// Package dto defines the JSON shapes the seasonfill HTTP layer
// emits and consumes. Types are framework-free apart from swag
// struct-tag annotations consumed by the spec generator.
package dto

import "time"

// OKResponse — canonical success envelope.
type OKResponse struct {
	OK bool `json:"ok" example:"true"`
}

// ErrorResponse — canonical 4xx/5xx envelope. Code is populated only
// for the auth 401; other handlers emit `{"error":msg}`. dto_test.go
// asserts byte parity with middleware/auth.go.
type ErrorResponse struct {
	Error string `json:"error" example:"unauthorized"`
	Code  string `json:"code,omitempty" example:"UNAUTHORIZED"`
}

// ScanConflictResponse — POST /scan 409 envelope. The `instance` key
// is preserved on the wire so clients can identify the busy target;
// dedicated DTO so the generated TS sees the field.
type ScanConflictResponse struct {
	Error    string `json:"error"    example:"scan already running"`
	Instance string `json:"instance" example:"alpha"`
	Code     string `json:"code"     example:"SCAN_IN_PROGRESS"`
}

// ScanNotFoundResponse — POST /scan 404 envelope (unknown instance).
// No Code field — historical wire shape is just error+instance.
type ScanNotFoundResponse struct {
	Error    string `json:"error"    example:"unknown instance"`
	Instance string `json:"instance" example:"alpha"`
}

// LoginRequest — optional JSON body for POST /auth/login.
type LoginRequest struct {
	APIKey string `json:"api_key" example:"sf_abc123"`
}

// ScanTriggerRequest — optional body for POST /scan.
// SeriesIDs narrows the scan to specific IDs; empty = all. Unknown
// IDs are skipped with a WARN (Q-010-3); response stays 202.
type ScanTriggerRequest struct {
	Instance  string `json:"instance,omitempty"   example:"alpha"`
	SeriesIDs []int  `json:"series_ids,omitempty"`
}

// ScanTriggerItem — one row in the 202 array response of POST /scan.
type ScanTriggerItem struct {
	ScanRunID    string    `json:"scan_run_id" example:"7b3d4a92-1234-4abc-9def-000000000001"`
	InstanceName string    `json:"instance"    example:"alpha"`
	Status       string    `json:"status"      example:"completed" enums:"completed,failed,running,aborted,cancelled"`
	Series       int       `json:"series_scanned"`
	Candidates   int       `json:"candidates_found"`
	Errors       int       `json:"errors"`
	Started      time.Time `json:"started_at"`
	Finished     time.Time `json:"finished_at"`
}

// Instance — Sonarr-instance health snapshot. Mode always emitted
// (Q-010-1) so the UI doesn't branch on field absence.
type Instance struct {
	Name             string     `json:"name"   example:"alpha"`
	Mode             string     `json:"mode"   example:"auto" enums:"auto,manual"`
	Health           string     `json:"health" example:"available" enums:"available,degraded,unavailable,unknown"`
	LastCheckAt      *time.Time `json:"last_check_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	TransitionsCount int        `json:"transitions_count"`
}

// InstanceList — body of GET /instances.
type InstanceList struct {
	Instances []Instance `json:"instances"`
}

// Scan — one ScanRun row.
type Scan struct {
	ID              string     `json:"id"               example:"7b3d4a92-1234-4abc-9def-000000000001"`
	Instance        string     `json:"instance"         example:"alpha"`
	Trigger         string     `json:"trigger"          example:"cron" enums:"cron,manual,webhook"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Status          string     `json:"status"           example:"completed" enums:"running,completed,failed,aborted,cancelled"`
	SeriesScanned   int        `json:"series_scanned"`
	CandidatesFound int        `json:"candidates_found"`
	GrabsPerformed  int        `json:"grabs_performed"`
	GrabsFailed     int        `json:"grabs_failed"`
	ErrorsCount     int        `json:"errors_count"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	DryRun          bool       `json:"dry_run"`
}

// Decision — one decisions row.
type Decision struct {
	ID              string    `json:"id"               example:"dec_001"`
	ScanRunID       string    `json:"scan_run_id"`
	Instance        string    `json:"instance"         example:"alpha"`
	SeriesID        int       `json:"series_id"`
	SeriesTitle     string    `json:"series_title"     example:"Severance"`
	SeasonNumber    int       `json:"season_number"`
	Decision        string    `json:"decision"         example:"grab" enums:"grab,skip,blocked_cooldown,already_optimal,expired"`
	Reason          string    `json:"reason"           example:"upgrade_available"`
	Category        string    `json:"category"         example:"action_taken" enums:"all_complete,sonarr_handles,action_taken,blocked,nothing_found,error,unknown"`
	MissingCount    int       `json:"missing_count"`
	ExistingCount   int       `json:"existing_count"`
	ReleasesFound   int       `json:"releases_found"`
	CandidatesCount int       `json:"candidates_count"`
	SelectedGUID    string    `json:"selected_guid,omitempty"`
	DryRunWouldGrab bool      `json:"dry_run_would_grab"`
	ErrorDetail     string    `json:"error_detail,omitempty" example:"sonarr: 503 service unavailable"`
	SupersededByID  string    `json:"superseded_by_id,omitempty" example:"7b3d4a92-1234-4abc-9def-000000000005"`
	CreatedAt       time.Time `json:"created_at"`
}

// Grab — one grab_records row.
type Grab struct {
	ID                string    `json:"id"                  example:"grb_001"`
	Instance          string    `json:"instance"            example:"alpha"`
	SeriesID          int       `json:"series_id"`
	SeriesTitle       string    `json:"series_title"        example:"Severance"`
	SeasonNumber      int       `json:"season_number"`
	ReleaseGUID       string    `json:"release_guid"`
	ReleaseTitle      string    `json:"release_title"`
	IndexerID         int       `json:"indexer_id"`
	IndexerName       string    `json:"indexer_name"        example:"tracker.x"`
	CustomFormatScore int       `json:"custom_format_score"`
	Quality           string    `json:"quality"             example:"WEBDL-1080p"`
	CoverageCount     int       `json:"coverage_count"`
	Status            string    `json:"status"              example:"imported" enums:"grabbed,imported,import_failed,grab_failed,expired"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	ScanRunID         string    `json:"scan_run_id"`
	Attempts          int       `json:"attempts"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ScanList — keyset-paginated GET /scans response.
type ScanList struct {
	Items      []Scan `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// DecisionList — keyset-paginated GET /decisions response.
type DecisionList struct {
	Items      []Decision `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// GrabList — keyset-paginated GET /grabs response.
type GrabList struct {
	Items      []Grab `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// HealthStatus — body of GET /healthz.
type HealthStatus struct {
	Status string `json:"status" example:"ok"`
}

// ReadyStatus — body of GET /readyz (200 or 503). Wire shape is
// snake_case throughout (`status,database,sonarr,instances,reasons`).
// The handler MUST marshal checker snapshots through dto.Instance —
// raw instance.Snapshot has no JSON tags and would leak PascalCase.
type ReadyStatus struct {
	Status    string     `json:"status"   example:"ok" enums:"ok,unavailable"`
	Database  bool       `json:"database" example:"true"`
	Sonarr    bool       `json:"sonarr"   example:"true"`
	Instances []Instance `json:"instances"`
	Reasons   []string   `json:"reasons,omitempty"`
}

// MissingSeasonStat — per-season aired-missing count. Season 0 = specials.
type MissingSeasonStat struct {
	SeasonNumber      int `json:"season_number"`
	MissingAiredCount int `json:"missing_aired_count"`
}

// MissingSeries — one row of GET /instances/:name/missing.
// TotalMissingAired is precomputed sum of Seasons[].MissingAiredCount.
type MissingSeries struct {
	SeriesID          int                 `json:"series_id"   example:"122"`
	Title             string              `json:"title"       example:"Severance"`
	Monitored         bool                `json:"monitored"   example:"true"`
	TotalMissingAired int                 `json:"total_missing_aired"`
	Seasons           []MissingSeasonStat `json:"seasons"`
}

// MissingSeriesList — body of GET /instances/:name/missing.
type MissingSeriesList struct {
	Items []MissingSeries `json:"items"`
	Total int             `json:"total"`
}

// SeriesSearchItem — one row of GET /instances/:name/series. Trimmed
// to picker-specific fields (Q-013a-3) so 013b's Combobox doesn't have
// to ignore noise. SeasonCount is monitored-season count; MissingAired
// is derived from series-level statistics (same source as Missing).
type SeriesSearchItem struct {
	SeriesID     int    `json:"series_id"            example:"122"`
	Title        string `json:"title"                example:"Severance"`
	Monitored    bool   `json:"monitored"            example:"true"`
	SeasonCount  int    `json:"season_count"         example:"2"`
	MissingAired int    `json:"missing_aired_count"  example:"8"`
}

// SeriesSearchList — body of GET /instances/:name/series. `Total` is
// the count BEFORE `limit` is applied so 013b can render
// "showing N of M". `Items` is empty-array-never-null (matches
// ScanList behaviour for TS codegen).
type SeriesSearchList struct {
	Items []SeriesSearchItem `json:"items"`
	Total int                `json:"total" example:"142"`
}
