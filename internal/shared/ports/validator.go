// Package ports holds shared infrastructure ports used across application
// layers. validator.go exposes the project-wide *validator.Validate
// singleton plus the public Validate(any) error entry point.
package ports

import (
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var (
	validateOnce sync.Once
	validate     *validator.Validate
)

// instance returns the process-wide *validator.Validate, initialising it on
// first call. Custom tag registrations (future) go inside the sync.Once
// closure so the singleton is built atomically.
//
// Two non-default setup steps:
//
//  1. RegisterTagNameFunc reads the `json` tag so FieldError.Field() returns
//     the JSON name ("instance_name") rather than the Go field name
//     ("InstanceName"). The wire response then matches the request shape.
//  2. WithRequiredStructEnabled (v10.16+) makes `required` on a struct-typed
//     field actually check the embedded struct is not the zero value;
//     otherwise the tag is silently ignored on structs.
func instance() *validator.Validate {
	validateOnce.Do(func() {
		validate = validator.New(validator.WithRequiredStructEnabled())
		validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "-" {
				return ""
			}
			return name
		})
	})
	return validate
}

// Validate runs struct-tag validation on s. Returns nil on success.
//
// On failure the typical concrete type is validator.ValidationErrors
// (slice of FieldError). Callers should errors.As it to render per-field
// messages. Other failure modes (InvalidValidationError when s is not a
// struct) surface as the validator package's own error types.
func Validate(s any) error {
	return instance().Struct(s)
}
