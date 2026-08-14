package obd

import (
	"context"
	"time"
)

// LinkType identifies how the adapter is connected.
type LinkType string

const (
	LinkUSB       LinkType = "usb"
	LinkBluetooth LinkType = "bluetooth"
	LinkMock      LinkType = "mock"
)

// DTCStatus mirrors common OBD-II code states.
type DTCStatus string

const (
	DTCPending   DTCStatus = "pending"
	DTCConfirmed DTCStatus = "confirmed"
	DTCPermanent DTCStatus = "permanent"
)

// DTC is a diagnostic trouble code observation.
type DTC struct {
	Code        string         `json:"code"`
	Status      DTCStatus      `json:"status"`
	FreezeFrame map[string]any `json:"freeze_frame,omitempty"`
	Description string         `json:"description,omitempty"`
}

// VehicleInfo is identity data readable from the ECU when available.
type VehicleInfo struct {
	VIN   string `json:"vin,omitempty"`
	ECU   string `json:"ecu,omitempty"`
	Proto string `json:"protocol,omitempty"`
}

// ScanResult is the payload produced by a single diagnostic pass.
type ScanResult struct {
	LinkType     LinkType      `json:"link_type"`
	AdapterName  string        `json:"adapter_name"`
	Protocol     string        `json:"protocol,omitempty"`
	Vehicle      VehicleInfo   `json:"vehicle"`
	DTCs         []DTC         `json:"dtcs"`
	Observations *Observations `json:"observations,omitempty"`
	ScannedAt    time.Time     `json:"scanned_at"`
	Duration     time.Duration `json:"duration"`
}

// Adapter talks to an OBD-II interface (USB ELM327, mock, later BT).
type Adapter interface {
	Name() string
	LinkType() LinkType
	Open(ctx context.Context) error
	Close() error
	Identify(ctx context.Context) (VehicleInfo, error)
	ReadDTCs(ctx context.Context) ([]DTC, error)
	ReadFreezeFrame(ctx context.Context, code string) (map[string]any, error)
}

// Scanner runs a diagnostic pass including live PID sampling when available.
type Scanner struct {
	Adapter  Adapter
	Progress func(string)
}

func (s *Scanner) report(msg string) {
	if s.Progress != nil {
		s.Progress(msg)
	}
}

func (s *Scanner) Scan(ctx context.Context) (*ScanResult, error) {
	start := time.Now()
	metrics := &LinkMetrics{Notes: []string{}}
	s.report("Opening adapter")
	if err := s.Adapter.Open(ctx); err != nil {
		return nil, err
	}
	defer s.Adapter.Close()

	s.report("Reading VIN and protocol")
	info, err := s.Adapter.Identify(ctx)
	metrics.CommandAttempts++
	if err != nil {
		metrics.CommandFailures++
		return nil, err
	}

	s.report("Reading trouble codes")
	dtcs, err := s.Adapter.ReadDTCs(ctx)
	metrics.CommandAttempts++
	if err != nil {
		metrics.CommandFailures++
		return nil, err
	}

	ffMap := map[string]map[string]any{}
	for i := range dtcs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.report("Freeze frame for " + dtcs[i].Code)
		metrics.CommandAttempts++
		ff, ferr := s.Adapter.ReadFreezeFrame(ctx, dtcs[i].Code)
		if ferr != nil {
			metrics.CommandFailures++
		} else if ff != nil {
			dtcs[i].FreezeFrame = ff
			ffMap[dtcs[i].Code] = ff
		}
		if dtcs[i].Description == "" {
			dtcs[i].Description = DescribeCode(dtcs[i].Code)
		}
	}

	obs := &Observations{
		FreezeFrames: ffMap,
		Link:         metrics,
	}
	if sampler, ok := s.Adapter.(LiveSampler); ok {
		s.report("Sampling live sensors")
		metrics.CommandAttempts++
		live, lerr := sampler.ReadLiveSnapshot(ctx)
		if lerr != nil {
			metrics.CommandFailures++
			metrics.Notes = append(metrics.Notes, "live_snapshot_partial")
		} else {
			obs.Live = live
		}
	}

	metrics.Protocol = info.Proto
	metrics.Confidence = linkConfidence(metrics)

	return &ScanResult{
		LinkType:     s.Adapter.LinkType(),
		AdapterName:  s.Adapter.Name(),
		Protocol:     info.Proto,
		Vehicle:      info,
		DTCs:         dtcs,
		Observations: obs,
		ScannedAt:    start.UTC(),
		Duration:     time.Since(start),
	}, nil
}

func linkConfidence(m *LinkMetrics) float64 {
	if m.CommandAttempts == 0 {
		return 0
	}
	ok := float64(m.CommandAttempts-m.CommandFailures) / float64(m.CommandAttempts)
	c := ok * 100
	if m.Timeouts > 0 {
		c -= float64(m.Timeouts) * 8
	}
	if c < 0 {
		c = 0
	}
	if c > 100 {
		c = 100
	}
	return c
}
