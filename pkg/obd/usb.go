package obd

import (
	"bufio"
	"context"
	"errors"
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
	info := InspectSerialPort(u.DevicePath)
	if info.Kind == KindJ2534OpenPort {
		return fmt.Errorf("%w (%s %s:%s %s)", ErrJ2534Unsupported, info.Path, info.VID, info.PID, info.Product)
	}
	if info.VID != "" {
		nick := USBNickname(info.VID, info.PID)
		if nick != "" {
			u.report(fmt.Sprintf("USB %s:%s %s (%s)", info.VID, info.PID, info.Kind, nick))
		} else {
			u.report(fmt.Sprintf("USB %s:%s %s", info.VID, info.PID, info.Kind))
		}
	}

	bauds := u.baudCandidates()
	if len(bauds) == 0 {
		bauds = []int{38400}
	}
	u.report(fmt.Sprintf("Opening %s at %d baud", u.DevicePath, bauds[0]))
	if err := u.openPort(ctx, bauds[0]); err != nil {
		return err
	}
	u.raiseModemLines()

	var last error
	for i, baud := range bauds {
		if err := ctx.Err(); err != nil {
			u.abandonPort()
			return err
		}
		if i > 0 {
			u.report(fmt.Sprintf("Trying %d baud (%d/%d) without reopening", baud, i+1, len(bauds)))
			if err := u.setBaud(baud); err != nil {
				last = err
				u.report(err.Error())
				continue
			}
		}
		if err := u.initELM(ctx); err != nil {
			last = err
			u.report(fmt.Sprintf("%d baud: %s", baud, err.Error()))
			if errors.Is(err, errStuckSerial) {
				u.abandonPort()
				return fmt.Errorf("%s: %w — unplug/replug the adapter", u.DevicePath, err)
			}
			continue
		}
		u.mu.Lock()
		u.ready = true
		u.mu.Unlock()
		return nil
	}
	u.abandonPort()
	if last == nil {
		last = fmt.Errorf("no response")
	}
	id := strings.TrimSpace(info.VID + ":" + info.PID)
	if nick := USBNickname(info.VID, info.PID); nick != "" && id != ":" {
		return fmt.Errorf("%s [%s %s]: %w — no ELM banner; unplug/replug, or use the dongle that answered ELM327 v1.5", u.DevicePath, nick, id, last)
	}
	return fmt.Errorf("%s: %w — unplug/replug the adapter, ignition ON, then try again", u.DevicePath, last)
}

func (u *USBAdapter) openPort(ctx context.Context, baud int) error {
	type opened struct {
		port serial.Port
		err  error
	}
	// QBD/CDC-ACM can take ~10s to open; do not wait for the 50s scan budget.
	openCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	ch := make(chan opened, 1)
	go func() {
		p, err := serial.Open(u.DevicePath, &serial.Mode{BaudRate: baud})
		ch <- opened{p, err}
	}()

	var port serial.Port
	select {
	case <-openCtx.Done():
		go func() {
			o := <-ch
			closeSerialAsync(o.port)
		}()
		if ctx.Err() != nil {
			return fmt.Errorf("open %s: %w", u.DevicePath, ctx.Err())
		}
		return fmt.Errorf("open %s: timed out after 12s — unplug/replug the adapter", u.DevicePath)
	case o := <-ch:
		if o.err != nil {
			return fmt.Errorf("open %s: %w", u.DevicePath, o.err)
		}
		port = o.port
	}

	_ = port.SetReadTimeout(150 * time.Millisecond)
	u.mu.Lock()
	u.port = port
	u.Baud = baud
	u.rw = bufio.NewReadWriter(bufio.NewReader(port), bufio.NewWriter(port))
	u.mu.Unlock()
	return nil
}

// abandonPort drops the handle without waiting. unix serial Close can block
// ~30s on a wedged CDC-ACM (J2534 clones, USB-reset mid-read).
func (u *USBAdapter) abandonPort() {
	u.mu.Lock()
	p := u.port
	u.port = nil
	u.rw = nil
	u.ready = false
	u.mu.Unlock()
	closeSerialAsync(p)
}

func (u *USBAdapter) Close() error {
	u.parkAdapter()
	u.abandonPort()
	return nil
}

func (u *USBAdapter) parkAdapter() {
	if !u.portOpen() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	u.report("Parking adapter")
	_, _ = u.command(ctx, "ATWS")
}

func closeSerialAsync(p serial.Port) {
	if p == nil {
		return
	}
	go func() { _ = p.Close() }()
}

func (u *USBAdapter) portOpen() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.port != nil
}

func (u *USBAdapter) setBaud(baud int) error {
	u.mu.Lock()
	port := u.port
	u.mu.Unlock()
	if port == nil {
		return fmt.Errorf("adapter not open")
	}
	if err := port.SetMode(&serial.Mode{BaudRate: baud}); err != nil {
		return fmt.Errorf("set baud %d: %w", baud, err)
	}
	u.Baud = baud
	_ = port.ResetInputBuffer()
	_ = port.ResetOutputBuffer()
	return nil
}

func (u *USBAdapter) raiseModemLines() {
	u.mu.Lock()
	port := u.port
	u.mu.Unlock()
	if port == nil {
		return
	}
	_ = port.SetDTR(true)
	_ = port.SetRTS(true)
}

func (u *USBAdapter) Identify(ctx context.Context) (VehicleInfo, error) {
	if err := u.ensureOpen(ctx); err != nil {
		return VehicleInfo{}, err
	}
	proto, err := u.findBus(ctx)
	if err != nil {
		return VehicleInfo{}, err
	}
	u.report("Asking ECU for protocol")
	desc, _ := u.command(ctx, "ATDP")
	if strings.TrimSpace(desc) == "" || strings.Contains(strings.ToUpper(desc), "AUTO") {
		desc = proto
	}
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
		Proto: strings.TrimSpace(desc),
		ECU:   "ELM327",
	}, nil
}

type elmBusProto struct {
	Code string
	Name string
}

// Phase 1 cars (2010+) are CAN. ATSP0 auto-search on cheap v1.5 clones often
// returns SEARCHING / UNABLE TO CONNECT even when the ECU is there.
var elmBusProtocols = []elmBusProto{
	{"6", "ISO 15765-4 CAN 11/500"},
	{"7", "ISO 15765-4 CAN 29/500"},
	{"8", "ISO 15765-4 CAN 11/250"},
	{"9", "ISO 15765-4 CAN 29/250"},
	{"0", "automatic"},
}

func pid0100Supported(resp string) bool {
	u := strings.ToUpper(strings.ReplaceAll(resp, " ", ""))
	if u == "" || strings.Contains(u, "UNABLE") || strings.Contains(u, "ERROR") || strings.Contains(u, "NODATA") {
		return false
	}
	return strings.Contains(u, "4100")
}

func (u *USBAdapter) findBus(ctx context.Context) (string, error) {
	var last error
	for _, p := range elmBusProtocols {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		u.report("Trying " + p.Name)
		if _, err := u.command(ctx, "ATSP"+p.Code); err != nil {
			last = err
			u.report(p.Name + ": " + err.Error())
			continue
		}
		resp, err := u.command(ctx, "0100")
		if pid0100Supported(resp) {
			u.report("ECU answered on " + p.Name)
			return p.Name, nil
		}
		if err != nil {
			last = err
			u.report(p.Name + ": " + clipLog(err.Error(), 80))
			continue
		}
		last = fmt.Errorf("no PID 00 (%q)", clipLog(resp, 40))
		u.report(p.Name + ": " + last.Error())
	}
	if last == nil {
		last = fmt.Errorf("no response")
	}
	return "", fmt.Errorf("no ECU on the bus (%w) — ignition ON, wait a few seconds after turning the key", last)
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
	u.report("Waking adapter")
	if err := u.wake(ctx); err != nil {
		return err
	}
	// Previous Close often pulses DTR; cheap CDC clones need a moment to boot.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(300 * time.Millisecond):
	}

	var id string
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		u.report("Adapter ATI")
		id, err = u.command(ctx, "ATI")
		if errors.Is(err, errStuckSerial) {
			return err
		}
		if looksLikeELM(id) {
			break
		}
		if attempt < 3 {
			u.report("ATI quiet, retrying")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(400 * time.Millisecond):
			}
			_ = u.wake(ctx)
		}
	}
	if !looksLikeELM(id) {
		u.report("No ELM banner from ATI — warm start")
		wid, werr := u.command(ctx, "ATWS")
		if errors.Is(werr, errStuckSerial) {
			return werr
		}
		if looksLikeELM(wid) {
			id, err = wid, nil
		} else {
			u.report("No ELM banner after ATWS — trying ATZ")
			zid, zerr := u.command(ctx, "ATZ")
			if errors.Is(zerr, errStuckSerial) {
				return zerr
			}
			if looksLikeELM(zid) {
				id, err = zid, nil
			} else if isPortDead(zerr) || isPortDead(err) || isPortDead(werr) {
				return u.reopenAfterUSBReset(ctx)
			} else if zerr != nil {
				return fmt.Errorf("ATZ: %w", zerr)
			} else if werr != nil {
				return fmt.Errorf("ATWS: %w", werr)
			} else if err != nil {
				return fmt.Errorf("ATI: %w", err)
			} else {
				return fmt.Errorf("not an ELM prompt (%q)", clipLog(zid, 80))
			}
		}
	}
	if err != nil {
		return fmt.Errorf("ATI: %w", err)
	}
	u.report("ELM identified: " + clipLog(id, 60))
	return u.configureELM(ctx)
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

func (u *USBAdapter) reopenAfterUSBReset(ctx context.Context) error {
	baud := u.Baud
	u.abandonPort()
	u.report("Adapter USB-reset after ATZ; waiting for the port")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(u.DevicePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ATZ: %s did not reappear", u.DevicePath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := u.openPort(ctx, baud); err != nil {
		return err
	}
	if err := u.wake(ctx); err != nil {
		return err
	}
	u.report("Adapter ATI")
	id, err := u.command(ctx, "ATI")
	if err != nil {
		return fmt.Errorf("ATI after ATZ: %w", err)
	}
	if !looksLikeELM(id) {
		return fmt.Errorf("not an ELM prompt after ATZ (%q)", clipLog(id, 80))
	}
	u.report("ELM identified: " + clipLog(id, 60))
	return u.configureELM(ctx)
}

func (u *USBAdapter) configureELM(ctx context.Context) error {
	for _, c := range []string{"ATE0", "ATL0", "ATS0", "ATH0", "ATAL"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		u.report("Adapter " + c)
		if _, err := u.command(ctx, c); err != nil {
			return fmt.Errorf("init %s: %w", c, err)
		}
	}
	// Max adaptive timeout; clones often ignore this.
	_, _ = u.command(ctx, "ATSTFF")
	return nil
}

func isPortDead(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "closed") ||
		strings.Contains(s, "disconnected") ||
		strings.Contains(s, "no such file")
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
	case "ATZ":
		return 2 * time.Second
	case "ATWS":
		return 1500 * time.Millisecond
	case "0100":
		return 8 * time.Second
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

var errStuckSerial = fmt.Errorf("serial read stuck in the kernel (Close would hang)")

func (u *USBAdapter) command(ctx context.Context, cmd string) (string, error) {
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
		// Do not Close() here. go.bug.st/serial Close waits for Read, and a
		// wedged CDC-ACM Read can sit in kernel select for ~30s.
		select {
		case o := <-ch:
			if o.s != "" {
				raw = o.s
				break
			}
			if raw == "" {
				return "", fmt.Errorf("no prompt for %s within %s", cmd, wait)
			}
		case <-time.After(250 * time.Millisecond):
			return "", fmt.Errorf("%w: no prompt for %s within %s", errStuckSerial, cmd, wait)
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
		if err == io.EOF || isPortDead(err) {
			if b.Len() > 0 {
				return b.String(), nil
			}
			return "", err
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
