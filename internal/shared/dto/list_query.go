package dto

// ListQuery is the canonical embedding for any list endpoint that wants
// instance scoping, tmdb-type narrowing, language preference, and pagination.
// Handlers embed ListQuery instead of redeclaring the four field groups —
// validator tags are inherited via embedding by go-playground/validator.
type ListQuery struct {
	InstanceFilter
	TmdbTypeFilter
	LanguagePref
	Pagination
}
