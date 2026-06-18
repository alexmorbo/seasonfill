package dto

// Pagination is the canonical page/per_page pair embedded into every list-
// endpoint request DTO. max=200 chosen per §6.3.1 (PRD prose 2230); larger
// pages reduce per-request overhead for catalog scans without blowing
// memory on small operator deployments.
type Pagination struct {
	Page    int `form:"page"     json:"page,omitempty"     validate:"omitempty,min=1"`
	PerPage int `form:"per_page" json:"per_page,omitempty" validate:"omitempty,min=1,max=200"`
}

// PerPageOrDefault returns the requested page size or 20 if unset/non-positive.
func (p Pagination) PerPageOrDefault() int {
	if p.PerPage <= 0 {
		return 20
	}
	return p.PerPage
}

// Offset returns the zero-based row offset for SQL OFFSET clauses. Pages are
// 1-indexed; page<=0 collapses to offset 0 (treated as the first page).
func (p Pagination) Offset() int {
	if p.Page <= 0 {
		return 0
	}
	return (p.Page - 1) * p.PerPageOrDefault()
}
