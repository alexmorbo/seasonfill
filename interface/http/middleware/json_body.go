package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// MaxJSONBodyBytes caps every JSON request body parsed via ReadJSONBody.
// 64 KiB is generous for instance / settings DTOs and small enough to
// reject DoS payloads cheaply at the parse stage.
const MaxJSONBodyBytes = 64 << 10

// ReadJSONBody enforces application/json + body cap + parses into out.
// Returns true on success. On any failure it writes a 400 with the legacy
// {error, code: BAD_REQUEST} envelope and returns false — callers MUST
// short-circuit:
//
//	if !middleware.ReadJSONBody(c, &req) { return }
//
// Was previously unexported in handlers/instances_crud.go; promoted here
// in F-3 so BindAndValidateJSON can compose it without circular package
// imports.
func ReadJSONBody(c *gin.Context, out any) bool {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "content-type must be application/json", Code: "BAD_REQUEST"})
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxJSONBodyBytes)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.AbortWithStatusJSON(http.StatusBadRequest,
				dto.ErrorResponse{Error: "payload too large", Code: "BAD_REQUEST"})
			return false
		}
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "cannot read body", Code: "BAD_REQUEST"})
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "malformed body", Code: "BAD_REQUEST"})
		return false
	}
	return true
}
