// row_config_handler.go ships GET /api/v1/discovery/rows (ADR-0017 D-1).
// Returns the effective row set: DB rows ordered by position if any exist,
// else the curated code-default set (domain.DefaultRows). Read-only in S1.
package rest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

// RowConfigLister is the narrow read port the handler needs. Satisfied by
// persistence.RowConfigRepository. Kept as an interface so the handler
// test passes a scripted fake (no DB).
type RowConfigLister interface {
	List(ctx context.Context) ([]disco.Row, error)
}

// RowConfigResponse is the wire envelope for GET /discovery/rows.
type RowConfigResponse struct {
	Rows []RowConfigItem `json:"rows"`
}

// RowConfigItem is one rail descriptor on the wire. ID is omitted for a
// code-default row (0 → not persisted). params is always a (possibly
// empty) object, never null.
type RowConfigItem struct {
	ID        int64             `json:"id,omitempty"`
	RowType   string            `json:"row_type"`
	Source    string            `json:"source"`
	MediaType string            `json:"media_type"`
	Params    map[string]string `json:"params"`
	Position  int               `json:"position"`
	Enabled   bool              `json:"enabled"`
	Title     string            `json:"title"`
}

// RowConfigHandler serves GET /api/v1/discovery/rows.
type RowConfigHandler struct {
	repo RowConfigLister
	log  *slog.Logger
}

// NewRowConfigHandler wires the handler. log MUST already carry the
// "discovery" domain tag (ports.DomainLogger(base, "discovery")).
func NewRowConfigHandler(repo RowConfigLister, log *slog.Logger) *RowConfigHandler {
	switch {
	case repo == nil:
		panic("row config handler: repo required")
	case log == nil:
		panic("row config handler: log required")
	}
	return &RowConfigHandler{repo: repo, log: log}
}

// Handle returns the effective row set. DB rows win when present; the
// code-default set is the empty-table fallback.
//
// No swagger annotation: sibling /discovery/* handlers are deliberately
// unannotated (FE hand-authors the discovery DTOs), so the row-config DTO
// is hand-authored in web/src/api/discoveryRows.ts (S1b) too. Keeping the
// route out of swagger.yaml matches the existing discovery convention.
func (h *RowConfigHandler) Handle(c *gin.Context) {
	rows, err := h.repo.List(c.Request.Context())
	if err != nil {
		h.log.WarnContext(c.Request.Context(), "discovery.rows.list_failed",
			slog.String("error", err.Error()))
		// Graceful degrade: serve the code-default set rather than 500 —
		// the rails page must always render something.
		rows = nil
	}
	if len(rows) == 0 {
		rows = disco.DefaultRows()
	}
	c.JSON(http.StatusOK, RowConfigResponse{Rows: projectRows(rows)})
}

func projectRows(rows []disco.Row) []RowConfigItem {
	out := make([]RowConfigItem, 0, len(rows))
	for _, r := range rows {
		params := r.Params
		if params == nil {
			params = map[string]string{}
		}
		out = append(out, RowConfigItem{
			ID:        r.ID,
			RowType:   string(r.RowType),
			Source:    string(r.Source),
			MediaType: string(r.MediaType),
			Params:    params,
			Position:  r.Position,
			Enabled:   r.Enabled,
			Title:     r.Title,
		})
	}
	return out
}
