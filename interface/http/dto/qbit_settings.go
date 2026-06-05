package dto

import "time"

// QbitSettingsDTO — body of GET /api/v1/instances/:name/qbit/settings
// and the response of PUT (same shape, post-persist). The password
// plaintext is NEVER returned. `password_set` is true iff the row
// has a non-empty encrypted blob.
type QbitSettingsDTO struct {
	ID                     uint      `json:"id"                          example:"1"`
	InstanceID             uint      `json:"instance_id"                 example:"7"`
	InstanceName           string    `json:"instance_name"               example:"alpha"`
	Enabled                bool      `json:"enabled"                     example:"true"`
	URL                    string    `json:"url"                         example:"http://qbit.local:8080"`
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
type QbitSettingsUpsertRequest struct {
	Enabled                bool     `json:"enabled"                     example:"true"`
	URL                    string   `json:"url"                         example:"http://qbit.local:8080"`
	Username               string   `json:"username,omitempty"          example:"admin"`
	Password               string   `json:"password,omitempty"          example:"hunter2"`
	Category               string   `json:"category"                    example:"sonarr"`
	PollIntervalMinutes    int      `json:"poll_interval_minutes"       example:"30"`
	RegrabCooldownHours    int      `json:"regrab_cooldown_hours"       example:"120"`
	MaxConsecutiveNoBetter int      `json:"max_consecutive_no_better"   example:"3"`
	CustomUnregisteredMsgs []string `json:"custom_unregistered_msgs"`
}
