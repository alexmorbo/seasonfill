package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

type GrabRepository struct {
	db *gorm.DB
}

func NewGrabRepository(db *gorm.DB) *GrabRepository {
	return &GrabRepository{db: db}
}

func (r *GrabRepository) Create(ctx context.Context, rec grab.Record) error {
	model := toGrabModel(rec)
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create grab_record: %w", err)
	}
	return nil
}

func toGrabModel(r grab.Record) database.GrabRecordModel {
	return database.GrabRecordModel{
		ID:                 r.ID.String(),
		InstanceName:       r.InstanceName,
		SeriesID:           r.SeriesID,
		SeriesTitle:        r.SeriesTitle,
		SeasonNumber:       r.SeasonNumber,
		ReleaseGUID:        r.ReleaseGUID,
		ReleaseTitle:       r.ReleaseTitle,
		DownloadID:         r.DownloadID,
		IndexerID:          r.IndexerID,
		IndexerName:        r.IndexerName,
		CustomFormatScore:  r.CustomFormatScore,
		Quality:            r.Quality,
		CoverageCount:      r.CoverageCount,
		Status:             string(r.Status),
		ErrorMessage:       r.ErrorMessage,
		ScanRunID:          r.ScanRunID.String(),
		Attempts:           r.Attempts,
		TorrentHash:        r.TorrentHash,
		ReplayOfID:         replayOfIDToString(r.ReplayOfID),
		SizeBytes:          r.SizeBytes,
		ParsedCodec:        parsedOptStr(r.Parsed, func(p grab.Parsed) string { return p.Codec }),
		ParsedSource:       parsedOptStr(r.Parsed, func(p grab.Parsed) string { return p.Source }),
		ParsedQuality:      parsedOptStr(r.Parsed, func(p grab.Parsed) string { return p.Quality }),
		ParsedResolution:   parsedOptInt(r.Parsed, func(p grab.Parsed) int { return p.Resolution }),
		ParsedHDRFlags:     parsedSlice(r.Parsed, func(p grab.Parsed) []string { return p.HDRFlags }),
		ParsedDub:          parsedOptStr(r.Parsed, func(p grab.Parsed) string { return p.Dub }),
		ParsedLanguages:    parsedSlice(r.Parsed, func(p grab.Parsed) []string { return p.Languages }),
		ParsedSubs:         parsedSlice(r.Parsed, func(p grab.Parsed) []string { return p.Subs }),
		ParsedReleaseGroup: parsedOptStr(r.Parsed, func(p grab.Parsed) string { return p.ReleaseGroup }),
		ParsedAt:           r.ParsedAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

func (r *GrabRepository) List(ctx context.Context, f ports.GrabFilter, p ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	if p.Limit <= 0 || p.Limit > ports.MaxListLimit {
		return nil, nil, fmt.Errorf("grab list: %w", ports.ErrInvalidLimit)
	}
	q := dbFromContext(ctx, r.db).WithContext(ctx).Model(&database.GrabRecordModel{})
	if f.Instance != nil {
		q = q.Where("instance_name = ?", *f.Instance)
	}
	if f.SeriesID != nil {
		q = q.Where("series_id = ?", *f.SeriesID)
	}
	if f.SeasonNumber != nil {
		q = q.Where("season_number = ?", *f.SeasonNumber)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at < ?", *f.To)
	}
	if p.Cursor != nil {
		q = q.Where("(created_at, id) < (?, ?)", p.Cursor.Timestamp, p.Cursor.ID)
	}
	var models []database.GrabRecordModel
	if err := q.Order("created_at DESC, id DESC").Limit(p.Limit + 1).Find(&models).Error; err != nil {
		return nil, nil, fmt.Errorf("grab list: %w", err)
	}
	var next *ports.Cursor
	if len(models) > p.Limit {
		last := models[p.Limit-1]
		next = &ports.Cursor{Timestamp: last.CreatedAt.UTC(), ID: last.ID}
		models = models[:p.Limit]
	}
	out := make([]grab.Record, 0, len(models))
	for _, m := range models {
		rec, err := toGrabRecord(m)
		if err != nil {
			return nil, nil, fmt.Errorf("grab list: %w", err)
		}
		out = append(out, rec)
	}
	return out, next, nil
}

func toGrabRecord(m database.GrabRecordModel) (grab.Record, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return grab.Record{}, fmt.Errorf("parse grab id: %w", err)
	}
	scanRunID, err := uuid.Parse(m.ScanRunID)
	if err != nil {
		return grab.Record{}, fmt.Errorf("parse scan_run_id: %w", err)
	}
	return grab.Record{
		ID:                id,
		InstanceName:      m.InstanceName,
		SeriesID:          m.SeriesID,
		SeriesTitle:       m.SeriesTitle,
		SeasonNumber:      m.SeasonNumber,
		ReleaseGUID:       m.ReleaseGUID,
		ReleaseTitle:      m.ReleaseTitle,
		DownloadID:        m.DownloadID,
		IndexerID:         m.IndexerID,
		IndexerName:       m.IndexerName,
		CustomFormatScore: m.CustomFormatScore,
		Quality:           m.Quality,
		CoverageCount:     m.CoverageCount,
		Status:            grab.Status(m.Status),
		ErrorMessage:      m.ErrorMessage,
		ScanRunID:         scanRunID,
		Attempts:          m.Attempts,
		TorrentHash:       m.TorrentHash,
		ReplayOfID:        parseReplayOfID(m.ReplayOfID),
		SizeBytes:         m.SizeBytes,
		Parsed:            parsedFromModel(m),
		ParsedAt:          m.ParsedAt,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}, nil
}

// parseReplayOfID is the *string → *uuid.UUID lift used by toGrabRecord.
// Nil ciphertext / unparseable text both return nil — the column is
// best-effort audit; we never block a row load on a malformed pointer.
func parseReplayOfID(s *string) *uuid.UUID {
	if s == nil || *s == "" {
		return nil
	}
	u, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &u
}

// MatchLatest implements the (downloadId-first, fallback-tuple) lookup
// described on ports.MatchKey. Two sequential SELECTs: the primary path
// short-circuits as soon as DownloadID resolves a non-terminal row; the
// fallback runs only when DownloadID is empty OR the primary missed.
func (r *GrabRepository) MatchLatest(ctx context.Context, key ports.MatchKey) (grab.Record, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	terminal := []string{
		string(grab.StatusImported),
		string(grab.StatusImportFailed),
		string(grab.StatusGrabFailed),
	}

	if key.DownloadID != "" {
		var m database.GrabRecordModel
		err := db.Model(&database.GrabRecordModel{}).
			Where("download_id = ? AND instance_name = ? AND status NOT IN ?",
				key.DownloadID, key.InstanceName, terminal).
			Order("created_at DESC, id DESC").
			First(&m).Error
		if err == nil {
			rec, convErr := toGrabRecord(m)
			if convErr != nil {
				return grab.Record{}, fmt.Errorf("match latest: %w", convErr)
			}
			return rec, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, fmt.Errorf("match latest by download_id: %w", err)
		}
		// fall through to fallback
	}

	if key.ReleaseTitle == "" || key.InstanceName == "" {
		return grab.Record{}, ports.ErrNotFound
	}

	var m database.GrabRecordModel
	err := db.Model(&database.GrabRecordModel{}).
		Where("release_title = ? AND series_id = ? AND season_number = ? AND instance_name = ? AND status NOT IN ?",
			key.ReleaseTitle, key.SeriesID, key.SeasonNumber, key.InstanceName, terminal).
		Order("created_at DESC, id DESC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, ports.ErrNotFound
		}
		return grab.Record{}, fmt.Errorf("match latest by tuple: %w", err)
	}
	rec, convErr := toGrabRecord(m)
	if convErr != nil {
		return grab.Record{}, fmt.Errorf("match latest: %w", convErr)
	}
	return rec, nil
}

// UpdateStatus writes status + error_message + updated_at. Defence-in-depth
// guard: reads current status and rejects the write via
// grab.ErrInvalidStatusTransition when the move is forbidden.
func (r *GrabRepository) UpdateStatus(ctx context.Context, id uuid.UUID, newStatus grab.Status, message string) error {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var current database.GrabRecordModel
	if err := db.Select("id", "status").First(&current, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("read grab status: %w", err)
	}

	if !grab.Status(current.Status).CanTransitionTo(newStatus) {
		return fmt.Errorf("%w: %q -> %q",
			grab.ErrInvalidStatusTransition, current.Status, string(newStatus))
	}

	now := time.Now().UTC()
	res := db.Model(&database.GrabRecordModel{}).
		Where("id = ?", id.String()).
		Updates(map[string]any{
			"status":        string(newStatus),
			"error_message": message,
			"updated_at":    now,
		})
	if res.Error != nil {
		return fmt.Errorf("update grab status: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// UpdateTorrentHash writes torrent_hash on a grab_records row when
// the column is currently NULL. Idempotent: a row whose hash is
// already set returns nil without overwriting (D63). Returns
// ports.ErrNotFound when the row id does not exist at all.
//
// hash is expected to be 40-char lowercase hex (the caller runs
// grab.ParseTorrentHash). An empty hash is a no-op success.
func (r *GrabRepository) UpdateTorrentHash(ctx context.Context, id uuid.UUID, hash string) error {
	if hash == "" {
		return nil
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var current database.GrabRecordModel
	if err := db.Select("id", "torrent_hash").First(&current, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("read grab torrent_hash: %w", err)
	}
	if current.TorrentHash != nil {
		// Already set by an earlier OnGrab delivery or by the grab
		// use case at insert. Never overwrite — D63 hash-required
		// gate makes the first-seen hash authoritative.
		return nil
	}

	now := time.Now().UTC()
	res := db.Model(&database.GrabRecordModel{}).
		Where("id = ? AND torrent_hash IS NULL", id.String()).
		Updates(map[string]any{
			"torrent_hash": hash,
			"updated_at":   now,
		})
	if res.Error != nil {
		return fmt.Errorf("update grab torrent_hash: %w", res.Error)
	}
	// RowsAffected == 0 means the SELECT-then-UPDATE race lost — another
	// caller set torrent_hash between our SELECT and UPDATE. That's the
	// intended idempotent outcome; not an error.
	return nil
}

// FindLatestSuccessByHash returns the newest non-failed grab_records row
// whose torrent_hash equals the supplied 40-char lowercase hex value.
// "Non-failed" excludes Status == grab_failed because those rows never
// represent an on-disk torrent — they're audit traces of failed
// force-grab attempts. imported / import_failed / grabbed rows are all
// candidates: even import_failed rows correspond to a real torrent the
// user still has in qBit (Sonarr's import failed but the file is on
// disk, just not where Sonarr expected). hash MUST be already
// normalised by the caller; empty hash returns ErrNotFound directly.
func (r *GrabRepository) FindLatestSuccessByHash(ctx context.Context, hash string) (grab.Record, error) {
	if hash == "" {
		return grab.Record{}, ports.ErrNotFound
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var m database.GrabRecordModel
	err := db.Model(&database.GrabRecordModel{}).
		Where("torrent_hash = ? AND status <> ?", hash, string(grab.StatusGrabFailed)).
		Order("created_at DESC, id DESC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, ports.ErrNotFound
		}
		return grab.Record{}, fmt.Errorf("find grab by torrent_hash: %w", err)
	}
	rec, convErr := toGrabRecord(m)
	if convErr != nil {
		return grab.Record{}, fmt.Errorf("find grab by torrent_hash: %w", convErr)
	}
	return rec, nil
}

// CreateReplay writes the row with ReplayOfID populated. Same INSERT
// path as Create — only difference is the explicit audit pointer. The
// rec.ReplayOfID field is overwritten with the supplied value so the
// caller can't accidentally pass a stale pointer.
func (r *GrabRepository) CreateReplay(ctx context.Context, rec grab.Record, replayOfID uuid.UUID) error {
	rec.ReplayOfID = &replayOfID
	return r.Create(ctx, rec)
}

// SetReplayOfID writes replay_of_id on an existing grab_records row.
// Idempotent: a row that already has a non-NULL value is left alone
// (defensive — concurrent regrab loops shouldn't be possible on the
// same triple due to the per-instance ticker, but the no-overwrite
// rule prevents accidental audit-pointer corruption).
func (r *GrabRepository) SetReplayOfID(ctx context.Context, id uuid.UUID, replayOfID uuid.UUID) error {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	now := time.Now().UTC()
	res := db.Model(&database.GrabRecordModel{}).
		Where("id = ? AND replay_of_id IS NULL", id.String()).
		Updates(map[string]any{
			"replay_of_id": replayOfID.String(),
			"updated_at":   now,
		})
	if res.Error != nil {
		return fmt.Errorf("set replay_of_id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// Could be: row missing OR row already had a value. Probe to
		// distinguish — gives ErrNotFound only on the true-missing path.
		var probe database.GrabRecordModel
		if err := db.Select("id").First(&probe, "id = ?", id.String()).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ports.ErrNotFound
			}
			return fmt.Errorf("probe after set replay_of_id: %w", err)
		}
		// Row exists but replay_of_id was already non-NULL — the
		// idempotent no-op success path.
	}
	return nil
}

// replayOfIDToString is the *uuid.UUID → *string lift used by toGrabModel.
// Nil in → nil out so the DB write hits a clean NULL.
func replayOfIDToString(u *uuid.UUID) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

// ListReplaysOf is the reverse-lookup of replay_of_id. One SQL round-
// trip per page (audit handler calls this once after fetching the
// page). Empty parentIDs returns the empty map without a SQL call.
// Each parent's child slice is capped at ports.MaxReplaysPerParent
// (PRD §9 risk #7). Partial index idx_grab_records_replay_of_id
// (migration 000007) covers the WHERE clause.
func (r *GrabRepository) ListReplaysOf(
	ctx context.Context, parentIDs []uuid.UUID,
) (map[uuid.UUID][]uuid.UUID, error) {
	out := make(map[uuid.UUID][]uuid.UUID, len(parentIDs))
	if len(parentIDs) == 0 {
		return out, nil
	}
	parentStrs := make([]string, 0, len(parentIDs))
	parentSet := make(map[string]uuid.UUID, len(parentIDs))
	for _, p := range parentIDs {
		s := p.String()
		parentStrs = append(parentStrs, s)
		parentSet[s] = p
	}
	type row struct {
		ID         string  `gorm:"column:id"`
		ReplayOfID *string `gorm:"column:replay_of_id"`
	}
	var rows []row
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	if err := db.Table("grab_records").
		Select("id", "replay_of_id").
		Where("replay_of_id IN ?", parentStrs).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list replays of: %w", err)
	}
	for _, ro := range rows {
		if ro.ReplayOfID == nil {
			continue
		}
		parent, ok := parentSet[*ro.ReplayOfID]
		if !ok {
			continue
		}
		child, err := uuid.Parse(ro.ID)
		if err != nil {
			continue
		}
		if len(out[parent]) >= ports.MaxReplaysPerParent {
			continue
		}
		out[parent] = append(out[parent], child)
	}
	return out, nil
}

// UpdateSizeBytes writes size_bytes when currently NULL. Idempotent:
// non-null returns nil. size <= 0 is a no-op success (Sonarr omits
// release.size sometimes; we never persist 0 B). Mirrors the
// UpdateTorrentHash first-seen-wins contract.
func (r *GrabRepository) UpdateSizeBytes(ctx context.Context, id uuid.UUID, size int64) error {
	if size <= 0 {
		return nil
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var current database.GrabRecordModel
	if err := db.Select("id", "size_bytes").First(&current, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("read grab size_bytes: %w", err)
	}
	if current.SizeBytes != nil {
		return nil
	}

	now := time.Now().UTC()
	res := db.Model(&database.GrabRecordModel{}).
		Where("id = ? AND size_bytes IS NULL", id.String()).
		Updates(map[string]any{"size_bytes": size, "updated_at": now})
	if res.Error != nil {
		return fmt.Errorf("update grab size_bytes: %w", res.Error)
	}
	return nil
}

// GetByID returns the grab_records row matching the supplied uuid.
// 043c: powers the episode-files endpoint lookup. Returns
// ports.ErrNotFound on miss. Other repo errors wrap with %w.
func (r *GrabRepository) GetByID(ctx context.Context, id uuid.UUID) (grab.Record, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var m database.GrabRecordModel
	if err := db.First(&m, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.Record{}, ports.ErrNotFound
		}
		return grab.Record{}, fmt.Errorf("get grab by id: %w", err)
	}
	return toGrabRecord(m)
}

// parsedFromModel reassembles a *grab.Parsed from the nullable model
// columns. Returns nil iff all parsed_* columns AND parsed_at are NULL —
// matches the "absent = pre-B2 row" invariant. A non-nil Parsed{} is
// possible when ParseRelease succeeded but returned nothing useful
// (parsed_at set, everything else NULL): 044b uses this to distinguish
// "parsed but empty" from "never parsed".
func parsedFromModel(m database.GrabRecordModel) *grab.Parsed {
	if m.ParsedAt == nil &&
		m.ParsedCodec == nil && m.ParsedSource == nil && m.ParsedQuality == nil &&
		m.ParsedResolution == nil && m.ParsedDub == nil && m.ParsedReleaseGroup == nil &&
		len(m.ParsedHDRFlags) == 0 && len(m.ParsedLanguages) == 0 && len(m.ParsedSubs) == 0 {
		return nil
	}
	p := grab.Parsed{
		HDRFlags:  append([]string(nil), m.ParsedHDRFlags...),
		Languages: append([]string(nil), m.ParsedLanguages...),
		Subs:      append([]string(nil), m.ParsedSubs...),
	}
	if m.ParsedCodec != nil {
		p.Codec = *m.ParsedCodec
	}
	if m.ParsedSource != nil {
		p.Source = *m.ParsedSource
	}
	if m.ParsedQuality != nil {
		p.Quality = *m.ParsedQuality
	}
	if m.ParsedResolution != nil {
		p.Resolution = *m.ParsedResolution
	}
	if m.ParsedDub != nil {
		p.Dub = *m.ParsedDub
	}
	if m.ParsedReleaseGroup != nil {
		p.ReleaseGroup = *m.ParsedReleaseGroup
	}
	return &p
}

func parsedOptStr(p *grab.Parsed, pick func(grab.Parsed) string) *string {
	if p == nil {
		return nil
	}
	v := pick(*p)
	if v == "" {
		return nil
	}
	return &v
}

func parsedOptInt(p *grab.Parsed, pick func(grab.Parsed) int) *int {
	if p == nil {
		return nil
	}
	v := pick(*p)
	if v == 0 {
		return nil
	}
	return &v
}

func parsedSlice(p *grab.Parsed, pick func(grab.Parsed) []string) []string {
	if p == nil {
		return nil
	}
	v := pick(*p)
	if len(v) == 0 {
		return nil
	}
	out := make([]string, len(v))
	copy(out, v)
	return out
}

// Ensure interface compliance at compile time.
var _ ports.GrabRepository = (*GrabRepository)(nil)
