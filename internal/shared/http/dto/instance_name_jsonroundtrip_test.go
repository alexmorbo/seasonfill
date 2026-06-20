package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// TestInstanceName_JSONRoundTrip locks in the contract that
// type InstanceName string marshals and unmarshals as a plain JSON string
// with no special handling — the A-5b-2 migration must NOT regress the
// wire shape. Multiple DTOs cover the InstanceName wire surface across
// scan/grab/decision/series_detail/counters/qbit responses.
func TestInstanceName_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
		want string // expected JSON fragment
	}{
		{
			name: "instance_counters_dto",
			in: dto.InstanceCountersDTO{
				InstanceName: domain.InstanceName("main"),
				Window:       "24h",
				AvgGrabs7d:   9.5,
			},
			want: `"instance_name":"main"`,
		},
		{
			name: "scan_trigger_item",
			in: dto.ScanTriggerItem{
				ScanRunID:    "7b3d4a92-1234-4abc-9def-000000000001",
				InstanceName: domain.InstanceName("anime"),
				Status:       "completed",
			},
			want: `"instance":"anime"`,
		},
		{
			name: "scan_conflict_response",
			in: dto.ScanConflictResponse{
				Error:    "scan already running",
				Instance: domain.InstanceName("kids"),
				Code:     "SCAN_IN_PROGRESS",
			},
			want: `"instance":"kids"`,
		},
		{
			name: "qbit_settings_dto",
			in: dto.QbitSettingsDTO{
				ID:           1,
				InstanceID:   7,
				InstanceName: domain.InstanceName("homelab"),
				URL:          "http://qbit.local:8080",
			},
			want: `"instance_name":"homelab"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.in)
			require.NoError(t, err)
			assert.Contains(t, string(raw), tt.want, "marshal shape")

			switch v := tt.in.(type) {
			case dto.InstanceCountersDTO:
				var out dto.InstanceCountersDTO
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.InstanceName, out.InstanceName)
			case dto.ScanTriggerItem:
				var out dto.ScanTriggerItem
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.InstanceName, out.InstanceName)
			case dto.ScanConflictResponse:
				var out dto.ScanConflictResponse
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.Instance, out.Instance)
			case dto.QbitSettingsDTO:
				var out dto.QbitSettingsDTO
				require.NoError(t, json.Unmarshal(raw, &out))
				assert.Equal(t, v.InstanceName, out.InstanceName)
			}
		})
	}
}
