package dto

// InstanceFilter scopes a list endpoint to a single instance by name.
// Instance names are operator-typed slugs (`sonarr-1`, `radarr_main`),
// hence alphanum_dash rather than the stricter built-in alphanum.
type InstanceFilter struct {
	Instance string `form:"instance" json:"instance,omitempty" validate:"omitempty,alphanum_dash"`
}
