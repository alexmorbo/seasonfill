package dto

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorResponse_MatchesMiddlewareEnvelope(t *testing.T) {
	t.Parallel()
	// middleware/auth.go emits {"error":"unauthorized","code":"AUTH_REQUIRED"}.
	got, err := json.Marshal(ErrorResponse{Error: "unauthorized", Code: "AUTH_REQUIRED"})
	require.NoError(t, err)
	var want, have map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"error":"unauthorized","code":"AUTH_REQUIRED"}`), &want))
	require.NoError(t, json.Unmarshal(got, &have))
	assert.Equal(t, want, have)
}

func TestErrorResponse_OmitsEmptyCode(t *testing.T) {
	t.Parallel()
	got, err := json.Marshal(ErrorResponse{Error: "invalid id"})
	require.NoError(t, err)
	assert.Equal(t, `{"error":"invalid id"}`, string(got))
}

func TestOKResponse_Roundtrip(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(OKResponse{OK: true})
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(raw))
}

func TestScanConflictResponse_Roundtrip(t *testing.T) {
	t.Parallel()
	in := ScanConflictResponse{Error: "scan already running", Instance: "alpha", Code: "SCAN_IN_PROGRESS"}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"error":"scan already running","instance":"alpha","code":"SCAN_IN_PROGRESS"}`, string(raw))
	var out ScanConflictResponse
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}

func TestScanNotFoundResponse_Roundtrip(t *testing.T) {
	t.Parallel()
	in := ScanNotFoundResponse{Error: "unknown instance", Instance: "alpha"}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"error":"unknown instance","instance":"alpha"}`, string(raw))
	// no `code` key in 404 envelope
	assert.NotContains(t, string(raw), `"code"`)
	var out ScanNotFoundResponse
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}

func TestReadyStatus_HasSnakeCaseKeys(t *testing.T) {
	t.Parallel()
	in := ReadyStatus{Status: "ok", Database: true}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	s := string(raw)
	for _, k := range []string{`"status"`, `"database"`} {
		assert.Contains(t, s, k, "missing snake_case key %s in wire", k)
	}
	for _, k := range []string{`"Status"`, `"Database"`} {
		assert.NotContains(t, s, k, "wire leaked PascalCase key %s", k)
	}
	// External-instance state intentionally not on /readyz — surfaced via
	// /api/v1/instances and the seasonfill_instance_health gauge.
	for _, k := range []string{`"sonarr"`, `"instances"`, `"reasons"`} {
		assert.NotContains(t, s, k, "/readyz body must not carry instance state: %s", k)
	}
}

func TestRoundtrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name string
		in   any
	}{
		{"Scan", Scan{ID: "u1", Instance: "alpha", Trigger: "manual", CreatedAt: now, StartedAt: now, Status: "completed", SeriesScanned: 42}},
		{"Decision", Decision{ID: "dec_1", ScanRunID: "u1", Instance: "alpha", SeriesID: 101, SeriesTitle: "X", SeasonNumber: 2, Decision: "grab", Reason: "up", CreatedAt: now}},
		{"Grab", Grab{ID: "grb_1", Instance: "alpha", SeriesID: 101, SeriesTitle: "X", SeasonNumber: 2, Status: "imported", Attempts: 1, CreatedAt: now, UpdatedAt: now}},
		{"Instance", Instance{Name: "alpha", Health: "available", TransitionsCount: 2}},
		{"ScanTriggerItem", ScanTriggerItem{ScanRunID: "u1", InstanceName: "alpha", Status: "completed"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.in)
			require.NoError(t, err)
			out := reflect.New(reflect.TypeOf(tc.in)).Interface()
			require.NoError(t, json.Unmarshal(raw, out))
			assert.Equal(t, tc.in, reflect.ValueOf(out).Elem().Interface())
		})
	}
}

func TestScanList_PreservesItemsKey(t *testing.T) {
	t.Parallel()
	// Empty list MUST serialize as items:[] not items:null — TS
	// generated type is `Scan[]`, never `null`.
	raw, err := json.Marshal(ScanList{Items: []Scan{}})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"items":[]`)
	// Non-empty list: open bracket follows the key directly.
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	raw2, err := json.Marshal(ScanList{Items: []Scan{{ID: "u1", Instance: "a", Trigger: "manual", CreatedAt: now, StartedAt: now, Status: "running"}}})
	require.NoError(t, err)
	assert.Contains(t, string(raw2), `"items":[`)
}

func TestInstance_OmitOptional(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(Instance{Name: "alpha", Health: "available"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "last_check_at")
	assert.NotContains(t, string(raw), "last_error")
}

func TestSeriesSearchItem_WireFormat(t *testing.T) {
	t.Parallel()
	in := SeriesSearchItem{
		SeriesID: 122, Title: "Severance",
		Monitored: true, SeasonCount: 2, MissingAired: 8,
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"series_id":122,"title":"Severance","monitored":true,"season_count":2,"missing_aired_count":8}`,
		string(raw))
	var out SeriesSearchItem
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}

func TestSeriesSearchList_PreservesItemsKey(t *testing.T) {
	t.Parallel()
	// Empty list MUST serialize as items:[] not items:null — TS
	// generated type is `SeriesSearchItem[]`, never null.
	raw, err := json.Marshal(SeriesSearchList{Items: []SeriesSearchItem{}})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"items":[]`)
	assert.Contains(t, string(raw), `"total":0`)
}
