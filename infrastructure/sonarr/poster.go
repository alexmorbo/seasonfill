package sonarr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

// PosterSize selects the Sonarr MediaCover variant.
type PosterSize string

const (
	PosterFull  PosterSize = "full"
	PosterSmall PosterSize = "small"
)

// MediaCoverResponse carries the streamed poster body + the upstream
// headers the handler forwards to the client. Body MUST be closed by
// the caller. NotModified=true means upstream returned 304 (because
// the caller passed If-None-Match) and Body is nil.
type MediaCoverResponse struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ETag          string
	NotModified   bool
}

// GetMediaCover streams the poster image from Sonarr's
// /api/v3/MediaCover/{seriesID}/poster[-500].jpg endpoint. Unlike the
// other Client methods, this one bypasses Client.do because do() forces
// JSON decode of the body. The rate-limiter (per-instance + global) is
// still honoured.
//
// ifNoneMatch is forwarded verbatim if non-empty; upstream 304 surfaces
// as MediaCoverResponse{NotModified: true}.
//
// Caller MUST close Body on success.
func (c *Client) GetMediaCover(ctx context.Context, seriesID int, size PosterSize, ifNoneMatch string) (*MediaCoverResponse, error) {
	var variant string
	switch size {
	case PosterSmall:
		variant = "poster-500.jpg"
	default:
		variant = "poster.jpg"
	}
	endpoint := "/api/v3/MediaCover/" + strconv.Itoa(seriesID) + "/" + variant
	full := c.baseURL + endpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", endpoint, err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "image/*")
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	if err := ratelimit.Wait(c.limiter, ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait %s: %w", endpoint, err)
	}
	if err := ratelimit.Wait(c.globalLimiter(), ctx); err != nil {
		return nil, fmt.Errorf("global rate limit wait %s: %w", endpoint, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		observability.SonarrAPIRequest(c.name, "/api/v3/MediaCover", "error")
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("call %s: %w", endpoint, ctxErr)
		}
		return nil, fmt.Errorf("call %s: %w", endpoint, errors.Join(err, domain.ErrInstanceNetwork))
	}
	observability.SonarrAPIRequest(c.name, "/api/v3/MediaCover", strconv.Itoa(resp.StatusCode))

	if resp.StatusCode == http.StatusNotModified {
		_ = resp.Body.Close()
		return &MediaCoverResponse{NotModified: true, ETag: resp.Header.Get("ETag")}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		se := &StatusError{Endpoint: endpoint, Status: resp.StatusCode, Body: string(body)}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("%w: %w", domain.ErrInstanceUnauthorized, se)
		}
		return nil, se
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" || strings.HasPrefix(ct, "text/") {
		ct = "image/jpeg"
	}
	return &MediaCoverResponse{
		Body:          resp.Body,
		ContentType:   ct,
		ContentLength: resp.ContentLength,
		ETag:          resp.Header.Get("ETag"),
	}, nil
}
