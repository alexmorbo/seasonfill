package dto

import "time"

// QbitSettingsDTO — body of GET /api/v1/instances/:name/qbit/settings
// and the response of PUT (same shape, post-persist). The password
// plaintext is NEVER returned. `password_set` is true iff the row
// has a non-empty encrypted blob.
//
// QbitPublicURL (082, F-P2-1) is the optional browser-reachable URL
// the SPA GrabDrawer "open in qBittorrent" link prefers when set,
// falling back to URL when empty. Always emitted (even when empty)
// so the frontend zod schema can rely on string-typed presence.
type QbitSettingsDTO struct {
	ID                     uint      `json:"id"                          example:"1"`
	InstanceID             uint      `json:"instance_id"                 example:"7"`
	InstanceName           string    `json:"instance_name"               example:"alpha"`
	Enabled                bool      `json:"enabled"                     example:"true"`
	URL                    string    `json:"url"                         example:"http://qbit.local:8080"`
	QbitPublicURL          string    `json:"qbit_public_url"             example:"https://qbit.example.com"`
	Username               string    `json:"username,omitempty"          example:"admin"`
	PasswordSet            bool      `json:"password_set"                example:"true"`
	Category               string    `json:"category"                    example:"sonarr"`
	PollIntervalMinutes    int       `json:"poll_interval_minutes"       example:"30"`
	RegrabCooldownHours    int       `json:"regrab_cooldown_hours"       example:"120"`
	MaxConsecutiveNoBetter int       `json:"max_consecutive_no_better"   example:"3"`
	CustomUnregisteredMsgs []string  `json:"custom_unregistered_msgs"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// QbitSettingsUpsertRequest — body of PUT
// /api/v1/instances/:name/qbit/settings. Password is the dirty-bit
// field: empty string means "keep existing on update / create as
// anon on first insert"; non-empty string is the new plaintext.
//
// QbitPublicURL (082, F-P2-1) is optional. Empty string clears any
// previously stored value; a non-empty string must parse as a valid
// http/https URL (rejected by the use case with wire code
// INVALID_QBIT_PUBLIC_URL).
type QbitSettingsUpsertRequest struct {
	Enabled                bool     `json:"enabled"                     example:"true"`
	URL                    string   `json:"url"                         example:"http://qbit.local:8080"`
	QbitPublicURL          string   `json:"qbit_public_url,omitempty"   example:"https://qbit.example.com"`
	Username               string   `json:"username,omitempty"          example:"admin"`
	Password               string   `json:"password,omitempty"          example:"hunter2"`
	Category               string   `json:"category"                    example:"sonarr"`
	PollIntervalMinutes    int      `json:"poll_interval_minutes"       example:"30"`
	RegrabCooldownHours    int      `json:"regrab_cooldown_hours"       example:"120"`
	MaxConsecutiveNoBetter int      `json:"max_consecutive_no_better"   example:"3"`
	CustomUnregisteredMsgs []string `json:"custom_unregistered_msgs"`
}
