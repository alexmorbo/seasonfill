// Package ports holds shared infrastructure ports used across application
// layers. validator.go exposes the project-wide *validator.Validate
// singleton plus the public Validate(any) error entry point.
package ports

import (
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var (
	validateOnce sync.Once
	validate     *validator.Validate
)

// bcp47LanguageTagPattern is a deliberately simplified subset of RFC 5646:
// 2- or 3-letter language ± optional 2-4 letter region/script subtag.
// Covers every value the operator UI sends today (`en`, `ru`, `pt-BR`,
// `zh-Hans`). If extlangs/variants/extensions arrive, swap in
// golang.org/x/text/language.Parse.
var bcp47LanguageTagPattern = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z]{2,4})?$`)

// alphanumDashPattern allows operator-typed instance slugs such as
// `sonarr-1`, `radarr_main`. Built-in `alphanum` rejects `-`/`_`, hence
// the custom tag.
var alphanumDashPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// instance returns the process-wide *validator.Validate, initialising it on
// first call. Custom tag registrations live inside the sync.Once closure so
// the singleton is built atomically.
//
// Three non-default setup steps:
//
//  1. RegisterTagNameFunc reads the `json` tag so FieldError.Field() returns
//     the JSON name ("instance_name") rather than the Go field name
//     ("InstanceName"). The wire response then matches the request shape.
//  2. WithRequiredStructEnabled (v10.16+) makes `required` on a struct-typed
//     field actually check the embedded struct is not the zero value;
//     otherwise the tag is silently ignored on structs.
//  3. RegisterValidation wires the two custom tags `bcp47_language_tag` and
//     `alphanum_dash` referenced by shared DTOs in internal/shared/dto/.
//     Without these, validator panics on first call with "Undefined tag".
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
		// Custom tags — both ignore non-string fields (return true) so
		// embedding with `omitempty` on the field tag is the only opt-out.
		_ = validate.RegisterValidation("bcp47_language_tag", func(fl validator.FieldLevel) bool {
			s, ok := fl.Field().Interface().(string)
			if !ok {
				return true
			}
			return bcp47LanguageTagPattern.MatchString(s)
		})
		_ = validate.RegisterValidation("alphanum_dash", func(fl validator.FieldLevel) bool {
			s, ok := fl.Field().Interface().(string)
			if !ok {
				return true
			}
			return alphanumDashPattern.MatchString(s)
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
