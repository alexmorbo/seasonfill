// Package tests — D-1-7b (story 460b) unit assertions for grab_records,
// episode_grabs, download_links. Inspects in-memory Schema(d) for both
// dialects; no DB.
package tests

import (
	"testing"

	atlasschema "ariga.io/atlas/sql/schema"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

func TestD1_7b_GrabRecords_TableShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "grab_records")
			if got := len(tbl.Columns); got != 32 {
				t.Errorf("grab_records column count = %d, want 32", got)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 1 || pk.Parts[0].C.Name != "id" {
				t.Errorf("grab_records PK = %+v, want single-column PK on id", pk)
			}
		})
	}
}

func TestD1_7b_GrabRecords_StatusCheck(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "grab_records")
	// 467a / D-6 corrected the enum to match the grab domain
	// constants (StatusGrabbed / StatusGrabFailed / StatusImported /
	// StatusImportFailed). The pre-D-6 enum
	// ('pending','grabbed','imported','failed','cancelled') drifted
	// from the domain and was never observed because the table sat
	// empty until the D-6 unskip.
	want := "status IN ('grabbed', 'grab_failed', 'imported', 'import_failed')"
	var got string
	for _, a := range tbl.Attrs {
		if chk, ok := a.(*atlasschema.Check); ok && chk.Name == "grab_records_status_check" {
			got = chk.Expr
		}
	}
	if got != want {
		t.Errorf("grab_records_status_check = %q, want %q", got, want)
	}
}

func TestD1_7b_GrabRecords_Indexes(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "grab_records")
	for _, name := range []string{
		"grab_records_inst_series_idx",
		"grab_records_dedupe_lookup_idx",
		"grab_records_release_guid_idx",
		"grab_records_download_id_idx",
		"grab_records_scan_run_idx",
		"grab_records_status_idx",
		"grab_records_inst_created_idx",
		"grab_records_replay_of_idx",
	} {
		_ = mustIndex(t, tbl, name)
	}
}

func TestD1_7b_GrabRecords_ReplayOfPartial(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "grab_records")
			idx := mustIndex(t, tbl, "grab_records_replay_of_idx")
			if idx.Unique {
				t.Errorf("grab_records_replay_of_idx should NOT be UNIQUE (audit pointer, multiple rows can replay)")
			}
			if got := indexPredicate(d, idx); got != "replay_of_id IS NOT NULL" {
				t.Errorf("grab_records_replay_of_idx predicate = %q, want %q",
					got, "replay_of_id IS NOT NULL")
			}
		})
	}
}

func TestD1_7b_GrabRecords_InstanceFK(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "grab_records")
	var fk *atlasschema.ForeignKey
	for _, candidate := range tbl.ForeignKeys {
		if candidate.Symbol == "grab_records_instance_name_fkey" {
			fk = candidate
			break
		}
	}
	if fk == nil {
		t.Fatal("grab_records_instance_name_fkey missing")
	}
	if fk.OnDelete != atlasschema.Cascade {
		t.Errorf("grab_records_instance_name_fkey OnDelete = %s, want CASCADE", fk.OnDelete)
	}
	if fk.RefTable.Name != "sonarr_instance" {
		t.Errorf("grab_records_instance_name_fkey ref = %s, want sonarr_instance", fk.RefTable.Name)
	}
}

func TestD1_7b_GrabRecords_ScanRunFKDeferred(t *testing.T) {
	t.Parallel()
	// scan_runs is NOT in schema until a later D-1 batch. The FK must
	// be tolerantly absent today; when scan_runs lands, addGrab will
	// declare the FK automatically and this test starts asserting
	// SetNull on it. Until then, just verify scan_run_id column ships
	// nullable without a constraint.
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "grab_records")
	scanRunCol := findColumnInTable(tbl, "scan_run_id")
	if scanRunCol == nil {
		t.Fatal("grab_records.scan_run_id column missing")
	}
	if scanRunCol.Type == nil || !scanRunCol.Type.Null {
		t.Errorf("grab_records.scan_run_id should be NULL-able")
	}
	for _, fk := range tbl.ForeignKeys {
		if fk.Symbol == "grab_records_scan_run_id_fkey" {
			// Future-proof branch: when scan_runs ships, FK must be SET NULL.
			if fk.OnDelete != atlasschema.SetNull {
				t.Errorf("grab_records_scan_run_id_fkey OnDelete = %s, want SET NULL", fk.OnDelete)
			}
			if fk.RefTable != nil && fk.RefTable.Name != "scan_runs" {
				t.Errorf("grab_records_scan_run_id_fkey ref = %s, want scan_runs", fk.RefTable.Name)
			}
			return
		}
	}
	// scan_runs absent today — FK deferred per addGrab doc body.
}

func TestD1_7b_EpisodeGrabs_TableShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "episode_grabs")
			if got := len(tbl.Columns); got != 5 {
				t.Errorf("episode_grabs column count = %d, want 5", got)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 2 {
				t.Fatalf("episode_grabs PK parts = %d, want 2", len(pk.Parts))
			}
			if pk.Parts[0].C.Name != "grab_id" || pk.Parts[1].C.Name != "episode_id" {
				t.Errorf("episode_grabs PK ordering = (%s, %s), want (grab_id, episode_id)",
					pk.Parts[0].C.Name, pk.Parts[1].C.Name)
			}
		})
	}
}

// TestD1_7b_EpisodeGrabs_GrabCascade — 467a / D-6 dropped the
// episode_grabs.episode_id FK to episodes(id) because the column
// stores Sonarr's surrogate episode id (NOT our canonical episodes.id).
// The remaining FK to grab_records(id) CASCADE is the load-bearing one
// — when a grab is deleted, its episode_grabs projection rows vanish
// with it.
func TestD1_7b_EpisodeGrabs_GrabCascade(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "episode_grabs")
	if len(tbl.ForeignKeys) != 1 {
		t.Fatalf("episode_grabs FK count = %d, want 1 (only grab_id FK after 467a)", len(tbl.ForeignKeys))
	}
	fk := tbl.ForeignKeys[0]
	if fk.Symbol != "episode_grabs_grab_id_fkey" {
		t.Errorf("episode_grabs FK symbol = %s, want episode_grabs_grab_id_fkey", fk.Symbol)
	}
	if fk.OnDelete != atlasschema.Cascade {
		t.Errorf("episode_grabs FK %s OnDelete = %s, want CASCADE", fk.Symbol, fk.OnDelete)
	}
}

func TestD1_7b_DownloadLinks_TableShape(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			tbl := mustTable(t, s, "download_links")
			if got := len(tbl.Columns); got != 11 {
				t.Errorf("download_links column count = %d, want 11", got)
			}
			pk := tbl.PrimaryKey
			if pk == nil || len(pk.Parts) != 1 || pk.Parts[0].C.Name != "qbit_hash" {
				t.Errorf("download_links PK = %+v, want single-column PK on qbit_hash", pk)
			}
		})
	}
}

func TestD1_7b_DownloadLinks_Checks(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "download_links")
	got := map[string]string{}
	for _, a := range tbl.Attrs {
		if chk, ok := a.(*atlasschema.Check); ok {
			got[chk.Name] = chk.Expr
		}
	}
	wantIDExpr := "((instance_type = 'sonarr' AND external_series_id IS NOT NULL AND external_movie_id IS NULL) " +
		"OR (instance_type = 'radarr' AND external_movie_id IS NOT NULL AND external_series_id IS NULL))"
	if got["download_links_type_id_check"] != wantIDExpr {
		t.Errorf("download_links_type_id_check = %q, want %q",
			got["download_links_type_id_check"], wantIDExpr)
	}
	if got["download_links_source_check"] != "source IN ('webhook', 'arr-poll', 'instance-backfill')" {
		t.Errorf("download_links_source_check = %q", got["download_links_source_check"])
	}
	if got["download_links_instance_type_check"] != "instance_type IN ('sonarr', 'radarr')" {
		t.Errorf("download_links_instance_type_check = %q", got["download_links_instance_type_check"])
	}
}

func TestD1_7b_DownloadLinks_GlobalSeriesSetNull(t *testing.T) {
	t.Parallel()
	s := schema.Schema(schema.DialectPostgres)
	tbl := mustTable(t, s, "download_links")
	var fk *atlasschema.ForeignKey
	for _, candidate := range tbl.ForeignKeys {
		if candidate.Symbol == "download_links_global_series_id_fkey" {
			fk = candidate
			break
		}
	}
	if fk == nil {
		t.Fatal("download_links_global_series_id_fkey missing")
	}
	if fk.OnDelete != atlasschema.SetNull {
		t.Errorf("download_links_global_series_id_fkey OnDelete = %s, want SET NULL", fk.OnDelete)
	}
	if fk.RefTable.Name != "series" {
		t.Errorf("download_links_global_series_id_fkey ref = %s, want series", fk.RefTable.Name)
	}
}

func TestD1_7b_TableCount_PostGrab(t *testing.T) {
	t.Parallel()
	for _, d := range dialects {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			s := schema.Schema(d)
			// Total table count bumped from 39 (post D-1-7b) to 41 after
			// D-1-7c (story 460c) added watchdog_state + watchdog_blacklist,
			// then to 42 after D-4 story 465b added scan_runs, then to 44
			// after D-5 story 466b added app_config + sonarr_instance_settings,
			// then to 47 after D-6 story 467a re-added the grab audit trio
			// (decisions, cooldowns, origin_releases), then to 51 after
			// D-6 story 467c added qbit_settings + qbit_torrents +
			// qbit_torrent_events + torrent_series_map, then to 52 after
			// D-7 story 468c re-added media_assets, then to 53 after
			// N-2a story 502 added discovery_lists, then to 54 after E-1
			// B3a added season_texts.
			if len(s.Tables) != 54 {
				t.Errorf("Schema(%s) tables = %d, want 54 (after E-1 B3a season_texts)", d, len(s.Tables))
			}
		})
	}
}
