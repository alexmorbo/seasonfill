package arrcore

// DTOs for the shared v3 endpoints. Byte-identical JSON tags to the former
// sonarr models. qualityNested / tagDTO / createTagRequest are intentionally
// duplicated from the sonarr package (arrcore is a leaf and must not import
// sonarr); JSON decode is structural so the duplication is behavior-identical.

type systemStatusDTO struct {
	Version     string `json:"version"`
	InstanceURL string `json:"instanceName"`
}

type qualityNested struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
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

type rootFolderDTO struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  int64  `json:"freeSpace"`
}

type tagDTO struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type createTagRequest struct {
	Label string `json:"label"`
}
