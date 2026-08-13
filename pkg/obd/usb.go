package obd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// USBAdapter talks to an ELM327-compatible adapter over USB serial (Linux CDC-ACM / FTDI / CP210x).
type USBAdapter struct {
	DevicePath string
	Baud       int
	NameHint   string

	mu   sync.Mutex
	port serial.Port
	rw   *bufio.ReadWriter
}

func NewUSBAdapter(devicePath string) *USBAdapter {
	return &USBAdapter{
		DevicePath: devicePath,
		Baud:       38400,
		NameHint:   "elm327-usb",
	}
}

func (u *USBAdapter) Name() string {
	if u.NameHint != "" {
		return u.NameHint
	}
	return "elm327-usb"
}

func (u *USBAdapter) LinkType() LinkType { return LinkUSB }

func (u *USBAdapter) Open(ctx context.Context) error {
	_ = ctx
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.port != nil {
		return nil
	}
	if u.DevicePath == "" {
		return fmt.Errorf("usb device path is required")
	}
	baud := u.Baud
	if baud == 0 {
		baud = 38400
	}
	mode := &serial.Mode{BaudRate: baud}
	port, err := serial.Open(u.DevicePath, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", u.DevicePath, err)
	}
	_ = port.SetReadTimeout(2 * time.Second)
	u.port = port
	u.rw = bufio.NewReadWriter(bufio.NewReader(port), bufio.NewWriter(port))

	if err := u.initELM(); err != nil {
		_ = port.Close()
		u.port = nil
		u.rw = nil
		return err
	}
	return nil
}

func (u *USBAdapter) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.port == nil {
		return nil
	}
	err := u.port.Close()
	u.port = nil
	u.rw = nil
	return err
}

func (u *USBAdapter) Identify(ctx context.Context) (VehicleInfo, error) {
	if err := u.ensureOpen(ctx); err != nil {
		return VehicleInfo{}, err
	}
	proto, _ := u.command(ctx, "ATDP")
	vin, _ := u.readVIN(ctx)
	return VehicleInfo{
		VIN:   strings.TrimSpace(vin),
		Proto: strings.TrimSpace(proto),
		ECU:   "ELM327",
	}, nil
}

func (u *USBAdapter) ReadDTCs(ctx context.Context) ([]DTC, error) {
	if err := u.ensureOpen(ctx); err != nil {
		return nil, err
	}

	type src struct {
		cmd    string
		status DTCStatus
	}
	sources := []src{
		{"03", DTCConfirmed},
		{"07", DTCPending},
		{"0A", DTCPermanent},
	}

	seen := map[string]DTC{}
	for _, s := range sources {
		resp, err := u.command(ctx, s.cmd)
		if err != nil {
			continue
		}
		for _, code := range DecodeDTCs(resp) {
			if existing, ok := seen[code]; ok {
				// Prefer confirmed over pending when both appear.
				if existing.Status == DTCConfirmed {
					continue
				}
			}
			seen[code] = DTC{
				Code:        code,
				Status:      s.status,
				Description: DescribeCode(code),
			}
		}
	}

	out := make([]DTC, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	return out, nil
}

func (u *USBAdapter) ReadFreezeFrame(ctx context.Context, code string) (map[string]any, error) {
	if err := u.ensureOpen(ctx); err != nil {
		return nil, err
	}
	// Mode 02 PID 00 availability probe; full PID decode is Phase 2.
	resp, err := u.command(ctx, "0200")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"code":       code,
		"raw_mode02": strings.TrimSpace(resp),
	}, nil
}

func (u *USBAdapter) ensureOpen(ctx context.Context) error {
	u.mu.Lock()
	opened := u.port != nil
	u.mu.Unlock()
	if opened {
		return nil
	}
	return u.Open(ctx)
}

func (u *USBAdapter) initELM() error {
	cmds := []string{
		"ATZ",  // reset
		"ATE0", // echo off
		"ATL0", // no linefeeds
		"ATS0", // no spaces
		"ATH0", // headers off
		"ATSP0", // auto protocol
	}
	for _, c := range cmds {
		if _, err := u.command(context.Background(), c); err != nil {
			return fmt.Errorf("init %s: %w", c, err)
		}
	}
	return nil
}

func (u *USBAdapter) readVIN(ctx context.Context) (string, error) {
	// Mode 09 PID 02 — VIN. Many ECUs return multi-frame; we do a best-effort parse.
	resp, err := u.command(ctx, "0902")
	if err != nil {
		return "", err
	}
	return extractVIN(resp), nil
}

func (u *USBAdapter) command(ctx context.Context, cmd string) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.port == nil || u.rw == nil {
		return "", fmt.Errorf("adapter not open")
	}

	_ = u.port.ResetInputBuffer()
	if _, err := io.WriteString(u.rw, cmd+"\r"); err != nil {
		return "", err
	}
	if err := u.rw.Flush(); err != nil {
		return "", err
	}

	deadline := time.Now().Add(3 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var b strings.Builder
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		_ = u.port.SetReadTimeout(200 * time.Millisecond)
		line, err := u.rw.ReadString('>')
		if len(line) > 0 {
			b.WriteString(line)
			if strings.Contains(line, ">") {
				break
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		// timeout: keep reading until deadline
	}
	out := b.String()
	out = strings.ReplaceAll(out, cmd, "")
	out = strings.ReplaceAll(out, ">", "")
	out = strings.TrimSpace(out)
	if strings.Contains(strings.ToUpper(out), "NO DATA") {
		return out, nil
	}
	if strings.Contains(strings.ToUpper(out), "UNABLE") || strings.Contains(strings.ToUpper(out), "ERROR") {
		return out, fmt.Errorf("adapter error: %s", out)
	}
	return out, nil
}

// ListUSBSerialDevices returns likely OBD adapter device nodes on Linux.
func ListUSBSerialDevices() ([]string, error) {
	patterns := []string{
		"/dev/ttyUSB*",
		"/dev/ttyACM*",
	}
	seen := map[string]struct{}{}
	var out []string
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if _, ok := seen[m]; ok {
				continue
			}
			if fi, err := os.Stat(m); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	return out, nil
}

func extractVIN(resp string) string {
	var alnum strings.Builder
	for _, r := range strings.ToUpper(resp) {
		if (r >= 'A' && r <= 'Z' && r != 'I' && r != 'O' && r != 'Q') || (r >= '0' && r <= '9') {
			alnum.WriteRune(r)
		}
	}
	s := alnum.String()
	// VIN is 17 chars; try to find a plausible window.
	for i := 0; i+17 <= len(s); i++ {
		cand := s[i : i+17]
		if looksLikeVIN(cand) {
			return cand
		}
	}
	return ""
}

func looksLikeVIN(s string) bool {
	if len(s) != 17 {
		return false
	}
	digit := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digit++
		}
	}
	return digit >= 3
}
