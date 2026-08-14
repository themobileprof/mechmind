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

	mu    sync.Mutex
	port  serial.Port
	rw    *bufio.ReadWriter
	ready bool
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

func (u *USBAdapter) baudCandidates() []int {
	seen := map[int]struct{}{}
	var out []int
	add := func(b int) {
		if b <= 0 {
			return
		}
		if _, ok := seen[b]; ok {
			return
		}
		seen[b] = struct{}{}
		out = append(out, b)
	}
	add(u.Baud)
	// USB ELM clones are often 115200; genuine ELM327 UART is often 38400.
	add(115200)
	add(38400)
	add(9600)
	return out
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

	var last error
	bauds := u.baudCandidates()
	for i, baud := range bauds {
		if err := ctx.Err(); err != nil {
			return err
		}
		u.report(fmt.Sprintf("Opening %s at %d baud (%d/%d)", u.DevicePath, baud, i+1, len(bauds)))
		if err := u.openPort(ctx, baud); err != nil {
			last = err
			u.report(err.Error())
			continue
		}
		if err := u.initELM(ctx); err != nil {
			last = err
			_ = u.Close()
			u.report(fmt.Sprintf("%d baud: %s", baud, err.Error()))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(350 * time.Millisecond):
			}
			continue
		}
		u.Baud = baud
		u.mu.Lock()
		u.ready = true
		u.mu.Unlock()
		return nil
	}
	if last == nil {
		last = fmt.Errorf("no response")
	}
	return fmt.Errorf("%s: %w — unplug/replug the adapter, ignition ON, then try again", u.DevicePath, last)
}

func (u *USBAdapter) openPort(ctx context.Context, baud int) error {
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

	_ = port.SetReadTimeout(150 * time.Millisecond)
	u.mu.Lock()
	u.port = port
	u.rw = bufio.NewReadWriter(bufio.NewReader(port), bufio.NewWriter(port))
	u.mu.Unlock()
	return nil
}

func (u *USBAdapter) forceClosePort() {
	u.mu.Lock()
	p := u.port
	u.port = nil
	u.rw = nil
	u.mu.Unlock()
	if p != nil {
		_ = p.Close()
	}
}

func (u *USBAdapter) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ready = false
	if u.port == nil {
		return nil
	}
	err := u.port.Close()
	u.port = nil
	u.rw = nil
	return err
}

func (u *USBAdapter) portOpen() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.port != nil
}

func (u *USBAdapter) Identify(ctx context.Context) (VehicleInfo, error) {
	if err := u.ensureOpen(ctx); err != nil {
		return VehicleInfo{}, err
	}
	// First OBD command after ATSP0 runs ELM protocol search (SEARCHING...).
	// That can take ~10–15s. Do it once, on 0100, not on VIN — and do not
	// tear down the serial port if VIN is slow or missing.
	u.report("Searching vehicle bus (up to 15s — keep ignition ON)")
	if _, err := u.command(ctx, "0100"); err != nil {
		return VehicleInfo{}, fmt.Errorf("no ECU on the bus: %w — ignition ON, wait a few seconds after turning the key", err)
	}
	u.report("Asking ECU for protocol")
	proto, _ := u.command(ctx, "ATDP")
	u.report("Asking ECU for VIN")
	vin, verr := u.readVIN(ctx)
	if verr != nil {
		u.report("VIN not returned: " + verr.Error())
		if !u.portOpen() {
			return VehicleInfo{}, verr
		}
	}
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
	ready := u.ready
	baud := u.Baud
	u.mu.Unlock()
	if opened {
		return nil
	}
	if ready && baud > 0 {
		u.report(fmt.Sprintf("Reconnecting %s at %d baud", u.DevicePath, baud))
		return u.openPort(ctx, baud)
	}
	return u.Open(ctx)
}

func (u *USBAdapter) initELM(ctx context.Context) error {
	// Do not send ATZ on USB. Cheap CDC-ACM clones USB-reset on ATZ, the
	// old ttyACM fd dies, and Read hangs until the whole scan times out.
	u.report("Waking adapter (skipping ATZ)")
	if err := u.wake(ctx); err != nil {
		return err
	}
	u.report("Adapter ATI")
	id, err := u.commandTimed(ctx, "ATI", true)
	if err != nil {
		return fmt.Errorf("ATI: %w", err)
	}
	if !looksLikeELM(id) {
		return fmt.Errorf("not an ELM prompt (%q)", clipLog(id, 80))
	}
	u.report("ELM identified: " + clipLog(id, 60))
	for _, c := range []string{"ATE0", "ATL0", "ATS0", "ATH0", "ATAL", "ATSP0"} {
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

func (u *USBAdapter) wake(ctx context.Context) error {
	u.mu.Lock()
	port := u.port
	u.mu.Unlock()
	if port == nil {
		return fmt.Errorf("adapter not open")
	}
	_, _ = port.Write([]byte("\r"))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(120 * time.Millisecond):
	}
	_ = port.ResetInputBuffer()
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

func commandWait(cmd string) time.Duration {
	switch cmd {
	case "0100":
		return 15 * time.Second
	case "0902":
		return 8 * time.Second
	case "03", "07", "0A":
		return 4 * time.Second
	case "ATSP0":
		return 2500 * time.Millisecond
	default:
		if len(cmd) >= 2 && cmd[0] >= '0' && cmd[0] <= '9' {
			return 2 * time.Second
		}
		return 1200 * time.Millisecond
	}
}

func (u *USBAdapter) command(ctx context.Context, cmd string) (string, error) {
	return u.commandTimed(ctx, cmd, false)
}

func (u *USBAdapter) commandTimed(ctx context.Context, cmd string, killOnTimeout bool) (string, error) {
	u.mu.Lock()
	port := u.port
	u.mu.Unlock()
	if port == nil {
		return "", fmt.Errorf("adapter not open")
	}

	_ = port.ResetInputBuffer()
	if _, err := port.Write([]byte(cmd + "\r")); err != nil {
		return "", err
	}

	wait := commandWait(cmd)
	cmdCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	type outcome struct {
		s   string
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		s, err := u.readUntilPrompt(cmdCtx, port)
		ch <- outcome{s, err}
	}()

	var raw string
	select {
	case <-cmdCtx.Done():
		if killOnTimeout {
			u.forceClosePort()
		}
		select {
		case o := <-ch:
			if o.s != "" {
				raw = o.s
				break
			}
		case <-time.After(200 * time.Millisecond):
			if killOnTimeout {
				return "", fmt.Errorf("no prompt for %s within %s", cmd, wait)
			}
			u.forceClosePort()
			return "", fmt.Errorf("no prompt for %s within %s", cmd, wait)
		}
		if raw == "" {
			return "", fmt.Errorf("no prompt for %s within %s", cmd, wait)
		}
	case o := <-ch:
		if o.err != nil {
			return "", o.err
		}
		raw = o.s
	}

	out := strings.ReplaceAll(raw, cmd, "")
	out = strings.ReplaceAll(out, ">", "")
	out = strings.TrimSpace(out)
	up := strings.ToUpper(out)
	if strings.Contains(up, "NO DATA") {
		return out, nil
	}
	if strings.Contains(up, "UNABLE") || strings.Contains(up, "ERROR") {
		return out, fmt.Errorf("adapter error: %s", out)
	}
	return out, nil
}

func (u *USBAdapter) readUntilPrompt(ctx context.Context, port serial.Port) (string, error) {
	_ = port.SetReadTimeout(120 * time.Millisecond)
	var b strings.Builder
	buf := make([]byte, 256)
	for {
		if err := ctx.Err(); err != nil {
			return b.String(), err
		}
		n, err := port.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
			if strings.Contains(b.String(), ">") {
				return b.String(), nil
			}
		}
		if err == io.EOF {
			if b.Len() > 0 {
				return b.String(), nil
			}
			return "", io.EOF
		}
	}
}

func looksLikeELM(s string) bool {
	u := strings.ToUpper(s)
	for _, n := range []string{"ELM", "STN", "OBDII", "OBD-II", "V1.", "V2.", "OK"} {
		if strings.Contains(u, n) {
			return true
		}
	}
	return false
}

func clipLog(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
