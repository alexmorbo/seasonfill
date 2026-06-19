package errors

import "fmt"

// GrabNotFoundError signals a missing grab_records row. ID is the UUID as
// a string (grab.Record.ID is uuid.UUID; the typed-id story for grab is
// reserved per ids.go GrabID godoc). Maps to HTTP 404.
type GrabNotFoundError struct {
	ID string
}

func (e *GrabNotFoundError) Error() string { return fmt.Sprintf("grab %q not found", e.ID) }

func (e *GrabNotFoundError) Code() string { return "grab_not_found" }

func (e *GrabNotFoundError) Retriable() bool { return false }
