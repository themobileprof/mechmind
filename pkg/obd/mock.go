package obd

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockAdapter simulates an ELM327 over USB for UI/API work without hardware.
type MockAdapter struct {
	VIN   string
	Codes []string

	mu     sync.Mutex
	opened bool
}

func NewMockAdapter(vin string, codes []string) *MockAdapter {
	if vin == "" {
		vin = "MOCKTESTVIN000001"
	}
	if len(codes) == 0 {
		codes = []string{"P0300", "P0171"}
	}
	return &MockAdapter{VIN: vin, Codes: codes}
}

func (m *MockAdapter) Name() string       { return "mock-elm327" }
func (m *MockAdapter) LinkType() LinkType { return LinkMock }

func (m *MockAdapter) Open(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened = true
	return nil
}

func (m *MockAdapter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.opened = false
	return nil
}

func (m *MockAdapter) Identify(ctx context.Context) (VehicleInfo, error) {
	if err := m.requireOpen(); err != nil {
		return VehicleInfo{}, err
	}
	select {
	case <-ctx.Done():
		return VehicleInfo{}, ctx.Err()
	case <-time.After(30 * time.Millisecond):
	}
	return VehicleInfo{
		VIN:   m.VIN,
		ECU:   "MOCK-ECM",
		Proto: "ISO 15765-4 (CAN)",
	}, nil
}

func (m *MockAdapter) ReadDTCs(ctx context.Context) ([]DTC, error) {
	if err := m.requireOpen(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(40 * time.Millisecond):
	}
	out := make([]DTC, 0, len(m.Codes))
	for _, c := range m.Codes {
		out = append(out, DTC{
			Code:        c,
			Status:      DTCConfirmed,
			Description: DescribeCode(c),
		})
	}
	return out, nil
}

func (m *MockAdapter) ReadFreezeFrame(ctx context.Context, code string) (map[string]any, error) {
	if err := m.requireOpen(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	// Freeze-frame at cruise-ish RPM so it can mismatch idle live sample.
	return map[string]any{
		"code":        code,
		"rpm":         2100.0,
		"load_pct":    45.0,
		"coolant_c":   91.0,
		"stft_b1_pct": 14.0,
		"ltft_b1_pct": 18.0,
		"speed_kmh":   48.0,
		"fuel_sys":    "closed_loop",
	}, nil
}

func (m *MockAdapter) ReadLiveSnapshot(ctx context.Context) (*LiveSnapshot, error) {
	if err := m.requireOpen(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(25 * time.Millisecond):
	}
	// Idle lean-biased sample so diagnosis rules can fire in mock tests.
	return &LiveSnapshot{
		RPM:           F64(820),
		SpeedKmh:      F64(0),
		LoadPct:       F64(28),
		CoolantC:      F64(92),
		IATC:          F64(34),
		MAFgS:         F64(2.1),
		MAPKPa:        F64(32),
		TPSPct:        F64(12),
		STFTB1Pct:     F64(18.5),
		LTFTB1Pct:     F64(16.2),
		O2B1S1V:       F64(0.12),
		O2B1S2V:       F64(0.55),
		ModuleVolts:   F64(13.9),
		FuelSysStatus: "closed_loop",
		SampledAt:     time.Now().UTC(),
	}, nil
}

func (m *MockAdapter) requireOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.opened {
		return fmt.Errorf("mock adapter is not open")
	}
	return nil
}
