package obd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AdapterKind is a coarse USB identity for listing and refusing the wrong protocol.
type AdapterKind string

const (
	KindUnknown       AdapterKind = "unknown"
	KindELMCandidate  AdapterKind = "elm_candidate"
	KindJ2534OpenPort AdapterKind = "j2534_openport"
)

// ErrJ2534Unsupported is returned when a Tactrix OpenPort-class adapter is used on the ELM path.
var ErrJ2534Unsupported = fmt.Errorf("this adapter is J2534 (Tactrix OpenPort-class), not ELM327; MechMind cannot scan it yet — use an ELM327/STN USB dongle")

// SerialPort is a candidate OBD USB serial node plus USB identity when known.
type SerialPort struct {
	Path         string      `json:"path"`
	Kind         AdapterKind `json:"kind"`
	VID          string      `json:"vid,omitempty"`
	PID          string      `json:"pid,omitempty"`
	Manufacturer string      `json:"manufacturer,omitempty"`
	Product      string      `json:"product,omitempty"`
	Label        string      `json:"label"`
}

// ClassifyUSBIdentity maps USB descriptors to an adapter kind.
// Tactrix OpenPort 2.0 (and typical clones) use FTDI 0403:cc4d.
func ClassifyUSBIdentity(vid, pid, manufacturer, product string) AdapterKind {
	vid = strings.ToLower(strings.TrimSpace(vid))
	pid = strings.ToLower(strings.TrimSpace(pid))
	blob := strings.ToLower(manufacturer + " " + product)
	if vid == "0403" && pid == "cc4d" {
		return KindJ2534OpenPort
	}
	if strings.Contains(blob, "openport") || strings.Contains(blob, "tactrix") {
		return KindJ2534OpenPort
	}
	if vid == "" && pid == "" {
		return KindUnknown
	}
	return KindELMCandidate
}

// USBNickname is a human name for known OBD USB IDs (not a protocol claim).
func USBNickname(vid, pid string) string {
	switch strings.ToLower(strings.TrimSpace(vid) + ":" + strings.TrimSpace(pid)) {
	case "0918:7104":
		return "QBD"
	case "0403:cc4d":
		return "Tactrix OpenPort"
	default:
		return ""
	}
}

func (p SerialPort) DisplayLabel() string {
	if p.Label != "" {
		return p.Label
	}
	switch p.Kind {
	case KindJ2534OpenPort:
		id := strings.TrimSpace(p.VID + ":" + p.PID)
		if id == ":" {
			id = "J2534"
		}
		return p.Path + " — OpenPort / J2534 (not ELM) " + id
	case KindELMCandidate:
		nick := USBNickname(p.VID, p.PID)
		id := strings.TrimSpace(p.VID + ":" + p.PID)
		switch {
		case nick != "" && id != ":":
			return p.Path + " — " + nick + " ELM-class " + id
		case p.VID != "":
			return p.Path + " — ELM-class " + p.VID + ":" + p.PID
		default:
			return p.Path + " — ELM-class"
		}
	default:
		return p.Path
	}
}

// InspectSerialPort fills USB identity for a tty/COM node.
func InspectSerialPort(path string) SerialPort {
	p := SerialPort{Path: path, Kind: KindUnknown}
	if runtime.GOOS != "linux" {
		p.Label = p.DisplayLabel()
		return p
	}
	vid, pid, mfr, prod := linuxUSBIdentity(path)
	p.VID, p.PID, p.Manufacturer, p.Product = vid, pid, mfr, prod
	p.Kind = ClassifyUSBIdentity(vid, pid, mfr, prod)
	p.Label = p.DisplayLabel()
	return p
}

// ListSerialPorts returns USB serial nodes with kind labels.
func ListSerialPorts() ([]SerialPort, error) {
	paths, err := ListUSBSerialDevices()
	if err != nil {
		return nil, err
	}
	out := make([]SerialPort, 0, len(paths))
	for _, path := range paths {
		out = append(out, InspectSerialPort(path))
	}
	return out, nil
}

func linuxUSBIdentity(devPath string) (vid, pid, mfr, product string) {
	base := filepath.Base(devPath)
	start := filepath.Join("/sys/class/tty", base, "device")
	dir, err := filepath.EvalSymlinks(start)
	if err != nil {
		dir = start
	}
	for i := 0; i < 10 && dir != "" && dir != "/"; i++ {
		v, verr := os.ReadFile(filepath.Join(dir, "idVendor"))
		p, perr := os.ReadFile(filepath.Join(dir, "idProduct"))
		if verr == nil && perr == nil {
			vid = strings.TrimSpace(string(v))
			pid = strings.TrimSpace(string(p))
			if b, err := os.ReadFile(filepath.Join(dir, "manufacturer")); err == nil {
				mfr = strings.TrimSpace(string(b))
			}
			if b, err := os.ReadFile(filepath.Join(dir, "product")); err == nil {
				product = strings.TrimSpace(string(b))
			}
			return vid, pid, mfr, product
		}
		dir = filepath.Dir(dir)
	}
	return "", "", "", ""
}
