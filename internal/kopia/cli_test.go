package kopia

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseContentStatsRaw(t *testing.T) {
	raw := `Count: 1482
Total Bytes: 104857600
Total Packed: 62914560 (compression 40.0%)
By Method:
  zstd                   count: 1400 size: 100000000 packed: 60000000 compression: 40.0%
Average: 70754
`
	st := parseContentStatsRaw(raw)
	if st.Count != 1482 {
		t.Fatalf("expected Count 1482, got %d", st.Count)
	}
	if st.OriginalBytes != 104857600 {
		t.Fatalf("expected OriginalBytes 104857600, got %d", st.OriginalBytes)
	}
	if st.PackedBytes != 62914560 {
		t.Fatalf("expected PackedBytes 62914560, got %d", st.PackedBytes)
	}

	// Uncompressed case where Total Packed line is omitted by Kopia
	uncomp := "Count: 5\nTotal Bytes: 4096\n"
	st2 := parseContentStatsRaw(uncomp)
	if st2.Count != 5 || st2.OriginalBytes != 4096 || st2.PackedBytes != 4096 {
		t.Fatalf("unexpected uncompressed stats: %+v", st2)
	}
}

func TestMaintenanceInfoJSONUnmarshal(t *testing.T) {
	sample := `{
		"owner": "alex@studio",
		"quick": { "enabled": true, "interval": 3600000000000 },
		"full": { "enabled": true, "interval": 86400000000000 },
		"extendObjectLocks": false,
		"schedule": {
			"nextFullMaintenance": "2026-11-10T02:00:00Z",
			"nextQuickMaintenance": "2026-11-09T03:00:00Z",
			"runs": {}
		}
	}`
	var mi MaintenanceInfo
	if err := json.Unmarshal([]byte(sample), &mi); err != nil {
		t.Fatalf("unmarshal MaintenanceInfo: %v", err)
	}
	if mi.Owner != "alex@studio" {
		t.Fatalf("expected owner alex@studio, got %q", mi.Owner)
	}
	if !mi.QuickCycle.Enabled || time.Duration(mi.QuickCycle.Interval) != time.Hour {
		t.Fatalf("unexpected QuickCycle: %+v", mi.QuickCycle)
	}
	if !mi.FullCycle.Enabled || time.Duration(mi.FullCycle.Interval) != 24*time.Hour {
		t.Fatalf("unexpected FullCycle: %+v", mi.FullCycle)
	}
	if mi.Schedule.NextQuickMaintenanceTime.IsZero() {
		t.Fatalf("expected NextQuickMaintenanceTime to be parsed")
	}
}
