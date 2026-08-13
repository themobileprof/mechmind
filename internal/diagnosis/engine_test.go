package diagnosis

import (
	"testing"

	"github.com/autoservice/autoservice/pkg/obd"
)

func TestIdleLeanFinding(t *testing.T) {
	in := Input{
		FocusCode: "P0171",
		DTCs: []obd.DTC{
			{Code: "P0171", Status: obd.DTCConfirmed},
			{Code: "P0300", Status: obd.DTCConfirmed},
		},
		Observations: &obd.Observations{
			Live: &obd.LiveSnapshot{
				RPM:       obd.F64(820),
				SpeedKmh:  obd.F64(0),
				STFTB1Pct: obd.F64(18),
				LTFTB1Pct: obd.F64(15),
				MAFgS:     obd.F64(2.0),
			},
			FreezeFrames: map[string]map[string]any{
				"P0171": {"rpm": 2100.0},
			},
			Link: &obd.LinkMetrics{CommandAttempts: 10, CommandFailures: 1, Confidence: 90},
		},
		CoOccur: []CoOccurStat{
			{Code: "P0300", WithCount: 8, FocusCount: 10, Rate: 0.8},
		},
	}
	findings := Analyze(in)
	if len(findings) < 2 {
		t.Fatalf("expected multiple findings, got %d %#v", len(findings), findings)
	}
	ids := map[string]bool{}
	for _, f := range findings {
		ids[f.ID] = true
	}
	if !ids["trim.idle_lean_unmetered_air"] {
		t.Fatalf("missing idle lean finding: %#v", findings)
	}
	if !ids["ff.condition_mismatch"] {
		t.Fatalf("missing freeze-frame mismatch: %#v", findings)
	}
	if !ids["fleet.cooccurrence"] {
		t.Fatalf("missing co-occurrence: %#v", findings)
	}
}
