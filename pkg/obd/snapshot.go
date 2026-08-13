package obd

import (
	"context"
	"time"
)

// LiveSnapshot is a Mode-01 style sample used for software diagnosis.
// Pointers distinguish "not collected" from zero.
type LiveSnapshot struct {
	RPM           *float64  `json:"rpm,omitempty"`
	SpeedKmh      *float64  `json:"speed_kmh,omitempty"`
	LoadPct       *float64  `json:"load_pct,omitempty"`
	CoolantC      *float64  `json:"coolant_c,omitempty"`
	IATC          *float64  `json:"iat_c,omitempty"`
	MAFgS         *float64  `json:"maf_gs,omitempty"`
	MAPKPa        *float64  `json:"map_kpa,omitempty"`
	TPSPct        *float64  `json:"tps_pct,omitempty"`
	STFTB1Pct     *float64  `json:"stft_b1_pct,omitempty"`
	LTFTB1Pct     *float64  `json:"ltft_b1_pct,omitempty"`
	STFTB2Pct     *float64  `json:"stft_b2_pct,omitempty"`
	LTFTB2Pct     *float64  `json:"ltft_b2_pct,omitempty"`
	O2B1S1V       *float64  `json:"o2_b1s1_v,omitempty"`
	O2B1S2V       *float64  `json:"o2_b1s2_v,omitempty"`
	ModuleVolts   *float64  `json:"module_voltage,omitempty"`
	FuelSysStatus string    `json:"fuel_sys_status,omitempty"`
	SampledAt     time.Time `json:"sampled_at,omitempty"`
}

// LinkMetrics captures communication health during the scan (soft wiring/gateway hints).
type LinkMetrics struct {
	CommandAttempts int      `json:"command_attempts"`
	CommandFailures int      `json:"command_failures"`
	Timeouts        int      `json:"timeouts"`
	Protocol        string   `json:"protocol,omitempty"`
	Confidence      float64  `json:"confidence"` // 0–100
	Notes           []string `json:"notes,omitempty"`
}

// Observations is the structured pack persisted with a scan for diagnosis rules.
type Observations struct {
	Live         *LiveSnapshot             `json:"live,omitempty"`
	FreezeFrames map[string]map[string]any `json:"freeze_frames,omitempty"`
	Link         *LinkMetrics              `json:"link,omitempty"`
	Readiness    []string                  `json:"readiness_incomplete,omitempty"`
}

// LiveSampler is implemented by adapters that can collect Mode-01 PIDs.
type LiveSampler interface {
	ReadLiveSnapshot(ctx context.Context) (*LiveSnapshot, error)
}

func F64(v float64) *float64 { return &v }
