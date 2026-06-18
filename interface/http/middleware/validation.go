// Package middleware (validation.go) exposes BindAndValidateJSON — the
// handler-level glue that parses + struct-validates request bodies and
// writes a structured 400 on validation failure.
//
// Response shape (verbatim from PRD §F-3):
//
//	{
//	  "error": "validation_failed",
//	  "fields": [
//	    {"field": "name", "tag": "required",
//	     "message": "name is required"}
//	  ]
//	}
//
// Localisation is OUT OF SCOPE — messages are English. See PRD §6.3 for
// the i18n follow-up.
//
// Integration with F-2a ErrorResponseMiddleware: this helper writes the
// 400 directly via c.AbortWithStatusJSON. It does NOT push a typed error
// via c.Error because the validation response shape is richer
// ({error, fields[]}) than F-2a's uniform {error, message} envelope.
// See story 389 Investigation §5 for the rationale.
package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// FieldError is the wire shape of one validation failure.
type FieldError struct {
	Field   string `json:"field"   example:"name"`
	Tag     string `json:"tag"     example:"required"`
	Message string `json:"message" example:"name is required"`
}

// ValidationFailedResponse is the body of a 400 from BindAndValidateJSON.
type ValidationFailedResponse struct {
	Error  string       `json:"error"  example:"validation_failed"`
	Fields []FieldError `json:"fields"`
}

// BindAndValidateJSON parses the JSON request body into out (must be a
// pointer to a struct) and runs struct-tag validation. On success returns
// true. On any failure (content-type, body cap, malformed JSON,
// validation) it writes a 400 and returns false — caller MUST short-
// circuit.
//
// Parse-stage failures emit the legacy {error, code: BAD_REQUEST} envelope
// to stay compatible with the existing ReadJSONBody contract. Validation
// failures emit the richer {error: "validation_failed", fields: [...]}
// envelope.
func BindAndValidateJSON(c *gin.Context, out any) bool {
	if !ReadJSONBody(c, out) {
		return false
	}

	if err := ports.Validate(out); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			c.AbortWithStatusJSON(http.StatusBadRequest, buildValidationResponse(verrs))
			return false
		}
		// Non-ValidationErrors (e.g. InvalidValidationError when out is
		// not a struct pointer) is a programmer error, not a client
		// mistake. 500.
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": "validation engine error: " + err.Error(),
		})
		return false
	}
	return true
}

// buildValidationResponse converts validator.ValidationErrors into the
// wire FieldError slice. Field name is the JSON tag (resolved via
// RegisterTagNameFunc in ports.instance), tag is the failed validator
// rule, message is the English-only rendered sentence.
func buildValidationResponse(verrs validator.ValidationErrors) ValidationFailedResponse {
	out := ValidationFailedResponse{
		Error:  "validation_failed",
		Fields: make([]FieldError, 0, len(verrs)),
	}
	for _, fe := range verrs {
		field := fe.Field()
		out.Fields = append(out.Fields, FieldError{
			Field:   field,
			Tag:     fe.Tag(),
			Message: messageFor(field, fe),
		})
	}
	return out
}

// messageFor returns the English error sentence for a failed field.
// Map keys are validator tag names. Unknown tags fall through to a
// generic message that includes the tag + param.
func messageFor(field string, fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "min":
		return fmt.Sprintf("%s must be at least %s", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", field, fe.Param())
	case "gt":
		return fmt.Sprintf("%s must be greater than %s", field, fe.Param())
	case "gte":
		return fmt.Sprintf("%s must be greater than or equal to %s", field, fe.Param())
	case "lt":
		return fmt.Sprintf("%s must be less than %s", field, fe.Param())
	case "lte":
		return fmt.Sprintf("%s must be less than or equal to %s", field, fe.Param())
	case "oneof":
		allowed := strings.Join(strings.Fields(fe.Param()), ", ")
		return fmt.Sprintf("%s must be one of: %s", field, allowed)
	case "url":
		return fmt.Sprintf("%s must be a valid URL", field)
	default:
		return fmt.Sprintf("%s failed validation %q", field, fe.Tag())
	}
}
