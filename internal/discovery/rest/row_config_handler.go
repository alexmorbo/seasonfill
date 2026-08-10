// row_config_handler.go ships GET /api/v1/discovery/rows (ADR-0017 D-1).
// Returns the effective row set: DB rows ordered by position if any exist,
// else the curated code-default set (domain.DefaultRows). Read-only in S1.
package rest

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

// RowConfigLister is the narrow read port the handler needs. Satisfied by
// persistence.RowConfigRepository. Kept as an interface so the handler
// test passes a scripted fake (no DB).
type RowConfigLister interface {
	List(ctx context.Context) ([]disco.Row, error)
}

// RowConfigWriter is the S2 write port (PUT/DELETE). Also satisfied by
// persistence.RowConfigRepository; wiring passes the same concrete repo as
// both lister and writer. Scripted-fake in the handler test.
type RowConfigWriter interface {
	Replace(ctx context.Context, rows []disco.Row) error
	DeleteAll(ctx context.Context) error
}

// RowConfigResponse is the wire envelope for GET/PUT /discovery/rows.
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

// RowConfigWriteRequest is the PUT body: the FULL ordered effective set. The
// FE materialises-on-first-edit — it PUTs the edited effective set (which
// includes the code-default rows when the table was empty). The BE just
// stores it verbatim (positions re-densified in the repo by slice order).
type RowConfigWriteRequest struct {
	Rows []RowConfigItem `json:"rows"`
}

// RowConfigHandler serves GET/PUT/DELETE /api/v1/discovery/rows.
type RowConfigHandler struct {
	repo   RowConfigLister
	writer RowConfigWriter
	log    *slog.Logger
}

// NewRowConfigHandler wires the handler. log MUST already carry the
// "discovery" domain tag (ports.DomainLogger(base, "discovery")). writer is
// required (S2 PUT/DELETE) — wiring passes the same concrete repo for both.
func NewRowConfigHandler(repo RowConfigLister, writer RowConfigWriter, log *slog.Logger) *RowConfigHandler {
	switch {
	case repo == nil:
		panic("row config handler: repo required")
	case writer == nil:
		panic("row config handler: writer required")
	case log == nil:
		panic("row config handler: log required")
	}
	return &RowConfigHandler{repo: repo, writer: writer, log: log}
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

// Save (PUT /discovery/rows) replaces the whole row config with the posted
// ordered set. Every row is enum-validated (row_type / source / media_type);
// an unknown value is a 400 (no partial write — validation runs before the
// repo tx). An empty rows array is valid and clears the table. On success
// returns 200 with the persisted set (dense positions, same shape as GET).
//
// No swagger annotation — sibling /discovery/* handlers are deliberately
// unannotated (the DTO is hand-authored in web/src/api/discoveryRows.ts).
func (h *RowConfigHandler) Save(c *gin.Context) {
	var req RowConfigWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_body", "malformed row config body")
		return
	}
	rows := make([]disco.Row, 0, len(req.Rows))
	for i, item := range req.Rows {
		rt := disco.RowType(item.RowType)
		src := disco.RowSource(item.Source)
		mt := disco.MediaType(item.MediaType)
		switch {
		case !rt.IsValid():
			respondError(c, http.StatusBadRequest, "invalid_row_type",
				"row "+strconv.Itoa(i)+": unknown row_type "+item.RowType)
			return
		case !src.IsValid():
			respondError(c, http.StatusBadRequest, "invalid_source",
				"row "+strconv.Itoa(i)+": unknown source "+item.Source)
			return
		case !mt.IsValid():
			respondError(c, http.StatusBadRequest, "invalid_media_type",
				"row "+strconv.Itoa(i)+": unknown media_type "+item.MediaType)
			return
		}
		params := item.Params
		if params == nil {
			params = map[string]string{}
		}
		rows = append(rows, disco.Row{
			RowType:   rt,
			Source:    src,
			MediaType: mt,
			Params:    params,
			Position:  i, // dense; repo re-asserts the same
			Enabled:   item.Enabled,
			Title:     item.Title,
		})
	}
	if err := h.writer.Replace(c.Request.Context(), rows); err != nil {
		h.log.WarnContext(c.Request.Context(), "discovery.rows.save_failed",
			slog.Int("count", len(rows)),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "save_failed", "could not persist row config")
		return
	}
	c.JSON(http.StatusOK, RowConfigResponse{Rows: projectRows(rows)})
}

// Reset (DELETE /discovery/rows) clears the table so GET falls back to the
// code-default set (domain.DefaultRows). Returns 204.
func (h *RowConfigHandler) Reset(c *gin.Context) {
	if err := h.writer.DeleteAll(c.Request.Context()); err != nil {
		h.log.WarnContext(c.Request.Context(), "discovery.rows.reset_failed",
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "reset_failed", "could not reset row config")
		return
	}
	c.Status(http.StatusNoContent)
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
