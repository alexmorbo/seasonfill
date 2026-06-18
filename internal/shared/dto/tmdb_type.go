package dto

// TmdbTypeFilter narrows a list endpoint to one TMDB media type
// (§5.1.4 enum: 0=Movie, 1=TV, 2=Person, 3=Anime-Movie, 4=Anime-Series,
// 5=Asian-Drama, 6=Documentary). Pointer is intentional: a non-pointer
// int could not distinguish "tmdb_type=0" (Movie filter) from "absent".
type TmdbTypeFilter struct {
	TmdbType *int `form:"tmdb_type" json:"tmdb_type,omitempty" validate:"omitempty,min=0,max=6"`
}
