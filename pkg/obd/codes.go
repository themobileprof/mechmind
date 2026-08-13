package obd

import "strings"

// SAE J2012-ish short descriptions for common codes.
// Full proprietary explanations live in the knowledge DB / AI layer.
var codeDescriptions = map[string]string{
	"P0300": "Random/Multiple Cylinder Misfire Detected",
	"P0301": "Cylinder 1 Misfire Detected",
	"P0302": "Cylinder 2 Misfire Detected",
	"P0303": "Cylinder 3 Misfire Detected",
	"P0304": "Cylinder 4 Misfire Detected",
	"P0171": "System Too Lean (Bank 1)",
	"P0172": "System Too Rich (Bank 1)",
	"P0420": "Catalyst System Efficiency Below Threshold (Bank 1)",
	"P0430": "Catalyst System Efficiency Below Threshold (Bank 2)",
	"P0455": "Evaporative Emission System Leak Detected (large leak)",
	"P0500": "Vehicle Speed Sensor Malfunction",
	"P0128": "Coolant Thermostat (Coolant Temp Below Thermostat Regulating Temperature)",
	"P0401": "Exhaust Gas Recirculation Flow Insufficient Detected",
	"P0442": "Evaporative Emission System Leak Detected (small leak)",
	"P0700": "Transmission Control System Malfunction",
	"C0035": "Left Front Wheel Speed Sensor Circuit",
	"B0001": "Driver Frontal Stage 1 Deployment Control",
	"U0100": "Lost Communication With ECM/PCM",
}

// DescribeCode returns a short SAE-style description when known.
func DescribeCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if d, ok := codeDescriptions[code]; ok {
		return d
	}
	switch {
	case strings.HasPrefix(code, "P0"), strings.HasPrefix(code, "P2"):
		return "Powertrain diagnostic trouble code"
	case strings.HasPrefix(code, "C"):
		return "Chassis diagnostic trouble code"
	case strings.HasPrefix(code, "B"):
		return "Body diagnostic trouble code"
	case strings.HasPrefix(code, "U"):
		return "Network/communication diagnostic trouble code"
	default:
		return "Diagnostic trouble code"
	}
}

// DecodeDTCs parses ELM327 mode 03/07/0A style hex payloads into codes.
// Accepts frames like "43 01 33 00 00" or multi-line responses.
func DecodeDTCs(payload string) []string {
	hex := make([]byte, 0, len(payload))
	for _, r := range strings.ToUpper(payload) {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
			hex = append(hex, byte(r))
		}
	}
	if len(hex) < 6 {
		return nil
	}

	// Drop service response byte (43/47/4A) when present at start of each frame chunk.
	raw := hexStringToBytes(string(hex))
	if len(raw) == 0 {
		return nil
	}

	out := make([]string, 0)
	i := 0
	if raw[0] == 0x43 || raw[0] == 0x47 || raw[0] == 0x4A {
		i = 1
	}
	for i+1 < len(raw) {
		a, b := raw[i], raw[i+1]
		i += 2
		if a == 0 && b == 0 {
			continue
		}
		out = append(out, encodeDTC(a, b))
	}
	return out
}

func encodeDTC(a, b byte) string {
	prefixes := []byte{'P', 'C', 'B', 'U'}
	prefix := prefixes[(a>>6)&0x03]
	nibble := (a >> 4) & 0x03
	return string([]byte{
		prefix,
		'0' + nibble,
		hexDigit(a & 0x0F),
		hexDigit((b >> 4) & 0x0F),
		hexDigit(b & 0x0F),
	})
}

func hexDigit(v byte) byte {
	if v < 10 {
		return '0' + v
	}
	return 'A' + (v - 10)
}

func hexStringToBytes(s string) []byte {
	if len(s)%2 == 1 {
		s = s[:len(s)-1]
	}
	out := make([]byte, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		hi := unhex(s[i])
		lo := unhex(s[i+1])
		if hi < 0 || lo < 0 {
			continue
		}
		out = append(out, byte(hi<<4|lo))
	}
	return out
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	default:
		return -1
	}
}
