package dto

// NotificationAgentView is the masked agent projection. NEVER carries the
// shoutrrr URL — only `configured` + a `scheme` hint.
type NotificationAgentView struct {
	ID         int64    `json:"id" example:"1"`
	Name       string   `json:"name" example:"telegram-main"`
	Enabled    bool     `json:"enabled" example:"true"`
	EventTypes []string `json:"event_types" example:"grab.failed,import.failed"`
	Configured bool     `json:"configured" example:"true"`
	Scheme     string   `json:"scheme" example:"telegram"`
}

type NotificationAgentListResponse struct {
	Agents []NotificationAgentView `json:"agents"`
}

// NotificationAgentCreateRequest — url required on create.
type NotificationAgentCreateRequest struct {
	Name       string   `json:"name" binding:"required" example:"telegram-main"`
	URL        string   `json:"url" binding:"required" example:"telegram://token@telegram?chats=123"`
	Enabled    bool     `json:"enabled" example:"true"`
	EventTypes []string `json:"event_types"`
}

// NotificationAgentUpdateRequest — url optional (empty = keep existing config).
type NotificationAgentUpdateRequest struct {
	Name       string   `json:"name" binding:"required" example:"telegram-main"`
	URL        string   `json:"url" example:""`
	Enabled    bool     `json:"enabled" example:"true"`
	EventTypes []string `json:"event_types"`
}

type NotificationTestResponse struct {
	OK bool `json:"ok" example:"true"`
}
