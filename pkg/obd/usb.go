package obd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	Progress   func(string)

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

func (u *USBAdapter) report(msg string) {
	if u.Progress != nil {
		u.Progress(msg)
	}
}

func (u *USBAdapter) Open(ctx context.Context) error {
	u.mu.Lock()
	if u.port != nil {
		u.mu.Unlock()
		return nil
	}
	u.mu.Unlock()

	if u.DevicePath == "" {
		return fmt.Errorf("usb device path is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	baud := u.Baud
	if baud == 0 {
		baud = 38400
	}
	u.report("Opening " + u.DevicePath)

	type opened struct {
		port serial.Port
		err  error
	}
	ch := make(chan opened, 1)
	go func() {
		p, err := serial.Open(u.DevicePath, &serial.Mode{BaudRate: baud})
		ch <- opened{p, err}
	}()

	var port serial.Port
	select {
	case <-ctx.Done():
		go func() {
			o := <-ch
			if o.port != nil {
				_ = o.port.Close()
			}
		}()
		return fmt.Errorf("open %s: %w", u.DevicePath, ctx.Err())
	case o := <-ch:
		if o.err != nil {
			return fmt.Errorf("open %s: %w", u.DevicePath, o.err)
		}
		port = o.port
	}

	_ = port.SetReadTimeout(400 * time.Millisecond)
	u.mu.Lock()
	u.port = port
	u.rw = bufio.NewReadWriter(bufio.NewReader(port), bufio.NewWriter(port))
	u.mu.Unlock()

	if err := u.initELM(ctx); err != nil {
		_ = u.Close()
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
	u.report("Asking ECU for protocol")
	proto, _ := u.command(ctx, "ATDP")
	u.report("Asking ECU for VIN")
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

func (u *USBAdapter) initELM(ctx context.Context) error {
	u.report("Resetting ELM327")
	cmds := []string{
		"ATZ",   // reset
		"ATE0",  // echo off
		"ATL0",  // no linefeeds
		"ATS0",  // no spaces
		"ATH0",  // headers off
		"ATSP0", // auto protocol
	}
	for _, c := range cmds {
		if err := ctx.Err(); err != nil {
			return err
		}
		u.report("Adapter " + c)
		if _, err := u.command(ctx, c); err != nil {
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
	port := u.port
	rw := u.rw
	u.mu.Unlock()
	if port == nil || rw == nil {
		return "", fmt.Errorf("adapter not open")
	}

	_ = port.ResetInputBuffer()
	if _, err := io.WriteString(rw, cmd+"\r"); err != nil {
		return "", err
	}
	if err := rw.Flush(); err != nil {
		return "", err
	}

	per := 1500 * time.Millisecond
	if cmd == "ATZ" {
		per = 4 * time.Second
	}
	deadline := time.Now().Add(per)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = port.Close()
		case <-done:
		}
	}()

	var b strings.Builder
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		_ = port.SetReadTimeout(200 * time.Millisecond)
		line, err := rw.ReadString('>')
		if len(line) > 0 {
			b.WriteString(line)
			if strings.Contains(line, ">") {
				break
			}
		}
		if err == io.EOF {
			break
		}
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

// ListUSBSerialDevices returns likely OBD adapter ports.
// Linux: /dev/ttyUSB* and /dev/ttyACM*. Windows: COM ports from the OS.
func ListUSBSerialDevices() ([]string, error) {
	if runtime.GOOS == "windows" {
		return serial.GetPortsList()
	}
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
