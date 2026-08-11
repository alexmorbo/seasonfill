package radarr

import (
	"context"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/arrcore"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Client is the Radarr v3 client. It embeds *arrcore.Client to inherit the
// shared transport + endpoints identical to Sonarr, and adds Radarr-domain
// movie endpoints. Ф6-R-3 / ADR-0018 §6b. arrcore is constructed with
// WithArrName("radarr") so StatusError + metrics carry the radarr label.
type Client struct {
	name   shareddomain.InstanceName
	logger *slog.Logger
	*arrcore.Client
}

var _ ports.RadarrClient = (*Client)(nil)

// Option configures a Client at construction. Alias of arrcore.Option so the
// radarr-side constructors mirror sonarr's option surface.
type Option = arrcore.Option

func WithGlobalLimiter(l *ratelimit.Limiter) Option { return arrcore.WithGlobalLimiter(l) }
func WithGlobalLimiterPointer(p *atomic.Pointer[ratelimit.Limiter]) Option {
	return arrcore.WithGlobalLimiterPointer(p)
}
func WithSearchTimeout(d time.Duration) Option { return arrcore.WithSearchTimeout(d) }

func New(name shareddomain.InstanceName, baseURL, apiKey string, timeout time.Duration, logger *slog.Logger) *Client {
	return NewWithOptions(name, baseURL, apiKey, timeout, nil, logger)
}

func NewWithLimiter(name shareddomain.InstanceName, baseURL, apiKey string, timeout time.Duration, limiter *ratelimit.Limiter, logger *slog.Logger) *Client {
	return NewWithOptions(name, baseURL, apiKey, timeout, limiter, logger)
}

// NewWithOptions constructs a Client, building the embedded arrcore transport
// with WithArrName("radarr") prepended so callers cannot accidentally drop the
// radarr label (their opts append after and can only override via WithArrName).
func NewWithOptions(name shareddomain.InstanceName, baseURL, apiKey string, timeout time.Duration, limiter *ratelimit.Limiter, logger *slog.Logger, opts ...Option) *Client {
	all := append([]Option{arrcore.WithArrName("radarr")}, opts...)
	return &Client{
		name:   name,
		logger: logger,
		Client: arrcore.New(name, baseURL, apiKey, timeout, limiter, all...),
	}
}

// Name returns the instance name. Shadows the promoted arrcore.Name() at depth
// 0 (unambiguous) and reads the retained radarr-side field.
func (c *Client) Name() string { return string(c.name) }

func (c *Client) get(ctx context.Context, endpoint string, q url.Values, out any) error {
	return c.Get(ctx, endpoint, q, out)
}

func (c *Client) post(ctx context.Context, endpoint string, body, out any) error {
	return c.Post(ctx, endpoint, body, out)
}

// LookupMovie calls GET /api/v3/movie/lookup?term={term}. For add-flow the
// caller passes term="tmdb:{id}". Radarr returns metadata for added or
// un-added candidates; empty result ([]) is non-error (caller surfaces 404).
func (c *Client) LookupMovie(ctx context.Context, term string) ([]ports.RadarrLookupResult, error) {
	q := url.Values{}
	q.Set("term", term)
	var dtos []movieLookupDTO
	if err := c.get(ctx, "/api/v3/movie/lookup", q, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.RadarrLookupResult, 0, len(dtos))
	for _, d := range dtos {
		img := d.RemotePoster
		if img == "" {
			for _, i := range d.Images {
				if i.CoverType == "poster" {
					if i.RemoteURL != "" {
						img = i.RemoteURL
					} else {
						img = i.URL
					}
					break
				}
			}
		}
		images := make([]ports.LookupImage, 0, len(d.Images))
		for _, i := range d.Images {
			images = append(images, ports.LookupImage{CoverType: i.CoverType, RemoteURL: i.RemoteURL, URL: i.URL})
		}
		out = append(out, ports.RadarrLookupResult{
			Title: d.Title, TitleSlug: d.TitleSlug, Year: d.Year,
			TMDBID: d.TMDBID, IMDBID: d.IMDBID, Overview: d.Overview,
			ImageURL: img, Images: images,
		})
	}
	return out, nil
}

// ListMovies calls GET /api/v3/movie — the full library, for the movie_states
// projection (radarr-sync, R-4 consumer; R-3 lands the client method).
func (c *Client) ListMovies(ctx context.Context) ([]ports.RadarrMovie, error) {
	var dtos []movieDTO
	if err := c.get(ctx, "/api/v3/movie", nil, &dtos); err != nil {
		return nil, err
	}
	out := make([]ports.RadarrMovie, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, movieDTOToPort(d))
	}
	return out, nil
}

// AddMovie posts to POST /api/v3/movie. minimumAvailability defaults to
// "released" (ADR-0018 Q3); monitored true; addOptions.searchForMovie from
// SearchOnAdd. Idempotent at the Radarr layer — a duplicate tmdbId returns a
// 400 with a "MovieExistsValidator" message which the use case maps to an
// already-added (non-error) result (§L3-3).
func (c *Client) AddMovie(ctx context.Context, p ports.AddMoviePayload) (ports.AddMovieResult, error) {
	minAvail := p.MinimumAvailability
	if minAvail == "" {
		minAvail = "released"
	}
	body := addMovieRequest{
		TMDBID:              p.TMDBID,
		Title:               p.Title,
		TitleSlug:           p.TitleSlug,
		Year:                p.Year,
		QualityProfileID:    p.QualityProfileID,
		RootFolderPath:      p.RootFolderPath,
		Monitored:           p.Monitored,
		MinimumAvailability: minAvail,
		AddOptions:          addMovieAddOptions{SearchForMovie: p.SearchOnAdd},
		Tags:                p.Tags,
	}
	if len(p.Images) > 0 {
		body.Images = make([]imageDTO, 0, len(p.Images))
		for _, img := range p.Images {
			body.Images = append(body.Images, imageDTO{CoverType: img.CoverType, URL: img.URL, RemoteURL: img.RemoteURL})
		}
	}
	var dto addMovieResponseDTO
	if err := c.post(ctx, "/api/v3/movie", body, &dto); err != nil {
		return ports.AddMovieResult{}, err
	}
	return ports.AddMovieResult{RadarrMovieID: dto.ID}, nil
}
