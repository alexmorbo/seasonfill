package domain

import "errors"

// Sentinel errors for the engine domain value objects. Callers may errors.Is
// against these; they never carry IO detail (domain is pure).
var (
	ErrInvalidMediaType = errors.New("mediadetail: invalid media type")
	ErrInvalidSection   = errors.New("mediadetail: invalid section")
	ErrInvalidMediaID   = errors.New("mediadetail: invalid media id")
)
