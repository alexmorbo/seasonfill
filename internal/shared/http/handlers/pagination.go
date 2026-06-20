package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
)

// HTTP pagination edge: tighter than ports.MaxListLimit (1000) so the
// public surface doesn't expose the defensive port ceiling.
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type listResponse struct {
	Items      any    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func writeListResponse(c *gin.Context, items any, next *ports.Cursor) {
	var cursorStr string
	if next != nil {
		cursorStr = next.String()
	}
	c.JSON(http.StatusOK, listResponse{Items: items, NextCursor: cursorStr})
}

func writeError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func parseLimit(c *gin.Context) (int, error) {
	raw := c.Query("limit")
	if raw == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxListLimit {
		return 0, ports.ErrInvalidLimit
	}
	return n, nil
}

func parseCursor(c *gin.Context) (*ports.Cursor, error) {
	return ports.ParseCursor(c.Query("cursor"))
}

func parseTimeRange(c *gin.Context) (from, to *time.Time, err error) {
	if raw := c.Query("from"); raw != "" {
		t, perr := time.Parse(time.RFC3339, raw)
		if perr != nil {
			return nil, nil, errInvalidParam("from")
		}
		t = t.UTC()
		from = &t
	}
	if raw := c.Query("to"); raw != "" {
		t, perr := time.Parse(time.RFC3339, raw)
		if perr != nil {
			return nil, nil, errInvalidParam("to")
		}
		t = t.UTC()
		to = &t
	}
	return from, to, nil
}

func parseOptionalInt(c *gin.Context, name string) (*int, error) {
	raw := c.Query(name)
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, errInvalidParam(name)
	}
	return &n, nil
}

// errInvalidParam carries the offending parameter name; the value is
// rendered as `invalid <name>` via Error().
type errInvalidParam string

func (e errInvalidParam) Error() string { return "invalid " + string(e) }

// handleQueryErr writes a 400 for a parse failure and returns true so
// the caller can return early. Returns false when err is nil.
func handleQueryErr(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ports.ErrInvalidLimit):
		writeError(c, http.StatusBadRequest, "invalid limit")
	case errors.Is(err, ports.ErrInvalidCursor):
		writeError(c, http.StatusBadRequest, "invalid cursor")
	default:
		var bad errInvalidParam
		if errors.As(err, &bad) {
			writeError(c, http.StatusBadRequest, bad.Error())
			return true
		}
		writeError(c, http.StatusBadRequest, err.Error())
	}
	return true
}
