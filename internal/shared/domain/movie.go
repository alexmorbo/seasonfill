package domain

// MovieID is the surrogate primary key of the movies canon table (Ф6-R-3).
// Separate ID space from SeriesID — movies are a distinct catalog vertical.
// The compiler refuses to mix MovieID with SeriesID even though both are
// int64 underneath (primitive-obsession defense, PRD §6.3.1 Level 2).
type MovieID int64
