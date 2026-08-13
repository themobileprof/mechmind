package obd

import (
	"context"
	"strings"
	"time"
)

// ReadLiveSnapshot samples common Mode 01 PIDs over ELM327 (best-effort).
func (u *USBAdapter) ReadLiveSnapshot(ctx context.Context) (*LiveSnapshot, error) {
	if err := u.ensureOpen(ctx); err != nil {
		return nil, err
	}
	snap := &LiveSnapshot{SampledAt: time.Now().UTC()}

	type pid struct {
		cmd string
		set func(string)
	}
	pids := []pid{
		{"010C", func(r string) { snap.RPM = decodeRPM(r) }},
		{"010D", func(r string) { snap.SpeedKmh = decodeByte(r, 1) }},
		{"0104", func(r string) { snap.LoadPct = decodePct(r) }},
		{"0105", func(r string) { snap.CoolantC = decodeTemp(r) }},
		{"010F", func(r string) { snap.IATC = decodeTemp(r) }},
		{"0110", func(r string) { snap.MAFgS = decodeMAF(r) }},
		{"010B", func(r string) { snap.MAPKPa = decodeByte(r, 1) }},
		{"0111", func(r string) { snap.TPSPct = decodePct(r) }},
		{"0106", func(r string) { snap.STFTB1Pct = decodeTrim(r) }},
		{"0107", func(r string) { snap.LTFTB1Pct = decodeTrim(r) }},
		{"0108", func(r string) { snap.STFTB2Pct = decodeTrim(r) }},
		{"0109", func(r string) { snap.LTFTB2Pct = decodeTrim(r) }},
		{"0114", func(r string) { snap.O2B1S1V = decodeO2(r) }},
		{"0115", func(r string) { snap.O2B1S2V = decodeO2(r) }},
		{"0142", func(r string) { snap.ModuleVolts = decodeVoltage(r) }},
	}
	for _, p := range pids {
		resp, err := u.command(ctx, p.cmd)
		if err != nil || resp == "" || strings.Contains(strings.ToUpper(resp), "NO DATA") {
			continue
		}
		p.set(resp)
	}
	return snap, nil
}

func decodePayload(resp string) []byte {
	hex := make([]byte, 0, len(resp))
	for i := 0; i < len(resp); i++ {
		c := resp[i]
		switch {
		case c >= '0' && c <= '9', c >= 'A' && c <= 'F', c >= 'a' && c <= 'f':
			hex = append(hex, c)
		}
	}
	if len(hex) < 6 {
		return nil
	}
	raw := hexStringToBytes(string(hex))
	// Expect 41 <pid> <data...>
	for i := 0; i+2 < len(raw); i++ {
		if raw[i] == 0x41 {
			return raw[i+2:]
		}
	}
	if len(raw) >= 3 {
		return raw[2:]
	}
	return raw
}

func decodeRPM(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 2 {
		return nil
	}
	v := float64(int(d[0])*256+int(d[1])) / 4.0
	return &v
}

func decodeByte(resp string, scale float64) *float64 {
	d := decodePayload(resp)
	if len(d) < 1 {
		return nil
	}
	v := float64(d[0]) * scale
	return &v
}

func decodePct(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 1 {
		return nil
	}
	v := float64(d[0]) * 100.0 / 255.0
	return &v
}

func decodeTemp(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 1 {
		return nil
	}
	v := float64(d[0]) - 40
	return &v
}

func decodeMAF(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 2 {
		return nil
	}
	v := float64(int(d[0])*256+int(d[1])) / 100.0
	return &v
}

func decodeTrim(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 1 {
		return nil
	}
	v := float64(d[0])/1.28 - 100.0
	return &v
}

func decodeO2(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 1 {
		return nil
	}
	v := float64(d[0]) / 200.0
	return &v
}

func decodeVoltage(resp string) *float64 {
	d := decodePayload(resp)
	if len(d) < 2 {
		return nil
	}
	v := float64(int(d[0])*256+int(d[1])) / 1000.0
	return &v
}
