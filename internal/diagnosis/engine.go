package diagnosis

import (
	"fmt"
	"math"
	"strings"

	"github.com/autoservice/autoservice/pkg/obd"
)

// Finding is an explainable software diagnosis hypothesis.
type Finding struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Confidence float64  `json:"confidence"` // 0–100
	Evidence   []string `json:"evidence"`
	RelatedDTCs []string `json:"related_dtcs,omitempty"`
	Severity   string   `json:"severity"` // info|watch|likely
}

// Input is everything the rule engine may use for one explain request.
type Input struct {
	FocusCode    string
	DTCs         []obd.DTC
	Observations *obd.Observations
	CoOccur      []CoOccurStat // precomputed from store
}

// CoOccurStat is fleet co-occurrence of another code with the focus code.
type CoOccurStat struct {
	Code       string  `json:"code"`
	WithCount  int     `json:"with_count"`
	FocusCount int     `json:"focus_count"`
	Rate       float64 `json:"rate"` // with/focus
}

// Analyze runs built-in rules and returns ranked findings.
func Analyze(in Input) []Finding {
	var out []Finding
	live := (*obd.LiveSnapshot)(nil)
	link := (*obd.LinkMetrics)(nil)
	if in.Observations != nil {
		live = in.Observations.Live
		link = in.Observations.Link
	}

	codes := map[string]obd.DTC{}
	for _, d := range in.DTCs {
		codes[strings.ToUpper(d.Code)] = d
	}
	focus := strings.ToUpper(in.FocusCode)

	out = append(out, trimIdleVsCruise(focus, codes, live)...)
	out = append(out, mafPlausibility(focus, codes, live)...)
	out = append(out, o2CatalystProxy(focus, codes, live)...)
	out = append(out, freezeFrameMismatch(focus, codes, in.Observations)...)
	out = append(out, misfireFingerprint(focus, codes, live)...)
	out = append(out, linkFragility(link)...)
	out = append(out, moduleVoltage(live)...)
	out = append(out, coOccurrenceFindings(focus, in.CoOccur)...)

	return rank(out)
}

func trimIdleVsCruise(focus string, codes map[string]obd.DTC, live *obd.LiveSnapshot) []Finding {
	if live == nil || live.STFTB1Pct == nil || live.LTFTB1Pct == nil {
		return nil
	}
	st, lt := *live.STFTB1Pct, *live.LTFTB1Pct
	rpm := 0.0
	if live.RPM != nil {
		rpm = *live.RPM
	}
	speed := 0.0
	if live.SpeedKmh != nil {
		speed = *live.SpeedKmh
	}
	idle := rpm > 0 && rpm < 1100 && speed < 3
	leanDTCs := hasAny(codes, "P0171", "P0174") || focus == "P0171" || focus == "P0174"

	var findings []Finding
	if leanDTCs && idle && st > 12 && lt > 10 {
		findings = append(findings, Finding{
			ID:         "trim.idle_lean_unmetered_air",
			Title:      "Idle-lean pattern suggests unmetered air",
			Summary:    "Short and long-term trims are strongly positive at idle. Prefer smoke-test / PCV / intake boots before replacing O2 sensors.",
			Confidence: 78,
			Severity:   "likely",
			RelatedDTCs: related(codes, "P0171", "P0174"),
			Evidence: []string{
				fmt.Sprintf("STFT B1=%.1f%% LTFT B1=%.1f%% at ~%.0f rpm", st, lt, rpm),
				"Idle / near-zero speed sample",
			},
		})
	}
	if leanDTCs && !idle && rpm > 1800 && st > 10 && lt > 10 {
		findings = append(findings, Finding{
			ID:         "trim.cruise_lean_fuel_or_maf",
			Title:      "Cruise-lean pattern suggests fuel delivery or MAF skew",
			Summary:    "Positive trims persist off-idle. Prioritize MAF rationality and fuel pressure over vacuum leaks alone.",
			Confidence: 72,
			Severity:   "likely",
			RelatedDTCs: related(codes, "P0171", "P0174"),
			Evidence: []string{
				fmt.Sprintf("STFT B1=%.1f%% LTFT B1=%.1f%% at ~%.0f rpm", st, lt, rpm),
			},
		})
	}
	if (focus == "P0172" || hasAny(codes, "P0172")) && st < -12 && lt < -8 {
		findings = append(findings, Finding{
			ID:         "trim.rich_bias",
			Title:      "Rich fuel-trim bias",
			Summary:    "Strongly negative trims. Check fuel pressure regulator, leaking injectors, or contaminated MAF before chasing O2.",
			Confidence: 70,
			Severity:   "likely",
			RelatedDTCs: related(codes, "P0172"),
			Evidence:   []string{fmt.Sprintf("STFT B1=%.1f%% LTFT B1=%.1f%%", st, lt)},
		})
	}
	return findings
}

func mafPlausibility(focus string, codes map[string]obd.DTC, live *obd.LiveSnapshot) []Finding {
	if live == nil || live.MAFgS == nil || live.RPM == nil {
		return nil
	}
	rpm := *live.RPM
	maf := *live.MAFgS
	if rpm < 600 {
		return nil
	}
	// Rough idle expectation ~2–7 g/s for many 4-cyl; cruise scales with RPM.
	expectedIdle := 3.5
	if live.LoadPct != nil && *live.LoadPct > 40 {
		expectedIdle = 8
	}
	ratio := maf / math.Max(expectedIdle*(rpm/800.0), 0.5)
	leanish := hasAny(codes, "P0171", "P0174") || focus == "P0171"
	if leanish && ratio < 0.55 {
		return []Finding{{
			ID:         "maf.low_vs_rpm",
			Title:      "MAF airflow low versus RPM expectation",
			Summary:    "Reported mass air flow is low for engine speed. Contaminated/skewed MAF or intake metering issue is plausible.",
			Confidence: 68,
			Severity:   "watch",
			RelatedDTCs: related(codes, "P0171", "P0174"),
			Evidence: []string{
				fmt.Sprintf("MAF=%.2f g/s at %.0f rpm (ratio≈%.2f vs simple model)", maf, rpm, ratio),
			},
		}}
	}
	if leanish && ratio > 1.8 {
		return []Finding{{
			ID:         "maf.high_vs_rpm",
			Title:      "MAF airflow high versus RPM expectation",
			Summary:    "MAF reports more air than expected. Possible MAF over-reporting or unmetered air compensated oddly — verify with speed-density cross-check.",
			Confidence: 60,
			Severity:   "watch",
			RelatedDTCs: related(codes, "P0171", "P0174"),
			Evidence:   []string{fmt.Sprintf("MAF=%.2f g/s at %.0f rpm (ratio≈%.2f)", maf, rpm, ratio)},
		}}
	}
	_ = focus
	return nil
}

func o2CatalystProxy(focus string, codes map[string]obd.DTC, live *obd.LiveSnapshot) []Finding {
	if live == nil || live.O2B1S1V == nil || live.O2B1S2V == nil {
		return nil
	}
	s1, s2 := *live.O2B1S1V, *live.O2B1S2V
	// Single sample is weak; use as soft proxy when P0420 present.
	if focus != "P0420" && !hasAny(codes, "P0420") {
		return nil
	}
	delta := math.Abs(s1 - s2)
	if delta < 0.05 && s2 > 0.4 && s2 < 0.7 {
		return []Finding{{
			ID:         "o2.rear_tracks_front",
			Title:      "Rear O2 mirrors front (catalyst efficiency soft signal)",
			Summary:    "With P0420, similar front/rear O2 levels on a snapshot can support inefficient cat storage — still confirm with waveform activity over time.",
			Confidence: 55,
			Severity:   "watch",
			RelatedDTCs: related(codes, "P0420"),
			Evidence:   []string{fmt.Sprintf("B1S1=%.3fV B1S2=%.3fV (|Δ|=%.3f)", s1, s2, delta)},
		}}
	}
	return nil
}

func freezeFrameMismatch(focus string, codes map[string]obd.DTC, obs *obd.Observations) []Finding {
	if obs == nil || obs.Live == nil {
		return nil
	}
	d, ok := codes[focus]
	if !ok || d.FreezeFrame == nil {
		// try observations map
		if obs.FreezeFrames != nil {
			if ff, ok := obs.FreezeFrames[focus]; ok {
				d.FreezeFrame = ff
			}
		}
	}
	if d.FreezeFrame == nil {
		return nil
	}
	ffRPM := asFloat(d.FreezeFrame["rpm"])
	liveRPM := obs.Live.RPM
	if ffRPM == nil || liveRPM == nil {
		return nil
	}
	if math.Abs(*ffRPM-*liveRPM) > 800 {
		return []Finding{{
			ID:         "ff.condition_mismatch",
			Title:      "Freeze-frame condition differs from live sample",
			Summary:    "The fault set under different speed/load than now. Treat as intermittent or condition-specific — reproduce the freeze-frame regime before parts replacement.",
			Confidence: 74,
			Severity:   "likely",
			RelatedDTCs: []string{focus},
			Evidence: []string{
				fmt.Sprintf("freeze-frame RPM=%.0f vs live RPM=%.0f", *ffRPM, *liveRPM),
			},
		}}
	}
	return nil
}

func misfireFingerprint(focus string, codes map[string]obd.DTC, live *obd.LiveSnapshot) []Finding {
	if focus != "P0300" && !hasAny(codes, "P0300") {
		return nil
	}
	cyl := []string{}
	for _, c := range []string{"P0301", "P0302", "P0303", "P0304", "P0305", "P0306"} {
		if hasAny(codes, c) {
			cyl = append(cyl, c)
		}
	}
	if len(cyl) == 1 {
		return []Finding{{
			ID:         "misfire.single_cylinder",
			Title:      "Single-cylinder misfire fingerprint",
			Summary:    "Random misfire accompanies one cylinder-specific code. Swap coil/plug to that cylinder before condemning fuel or ECM.",
			Confidence: 80,
			Severity:   "likely",
			RelatedDTCs: append([]string{"P0300"}, cyl...),
			Evidence:   []string{"Co-present cylinder DTC: " + strings.Join(cyl, ", ")},
		}}
	}
	if len(cyl) == 0 && live != nil && live.STFTB1Pct != nil && *live.STFTB1Pct > 12 {
		return []Finding{{
			ID:         "misfire.with_lean_trims",
			Title:      "Random misfire with lean trims",
			Summary:    "P0300 without cylinder IDs plus lean trims often tracks fuel/air issues more than a single bad coil.",
			Confidence: 66,
			Severity:   "watch",
			RelatedDTCs: related(codes, "P0300", "P0171"),
			Evidence:   []string{fmt.Sprintf("STFT B1=%.1f%% with P0300", *live.STFTB1Pct)},
		}}
	}
	return nil
}

func linkFragility(link *obd.LinkMetrics) []Finding {
	if link == nil {
		return nil
	}
	if link.Confidence > 0 && link.Confidence < 55 {
		return []Finding{{
			ID:         "link.fragile",
			Title:      "Fragile OBD link during scan",
			Summary:    "Elevated command failures/timeouts. Soft hint of gateway sleep, adapter quality, or intermittent wiring — do not trust incomplete PID packs alone.",
			Confidence: 62,
			Severity:   "watch",
			Evidence: []string{
				fmt.Sprintf("attempts=%d failures=%d timeouts=%d confidence=%.0f",
					link.CommandAttempts, link.CommandFailures, link.Timeouts, link.Confidence),
			},
		}}
	}
	return nil
}

func moduleVoltage(live *obd.LiveSnapshot) []Finding {
	if live == nil || live.ModuleVolts == nil {
		return nil
	}
	v := *live.ModuleVolts
	if v > 0 && v < 11.8 {
		return []Finding{{
			ID:         "power.low_module_voltage",
			Title:      "Low control-module voltage",
			Summary:    "Module voltage under ~11.8V can cause ghost U-codes and flaky communications. Stabilize battery/charging before deep electrical diagnosis.",
			Confidence: 76,
			Severity:   "likely",
			Evidence:   []string{fmt.Sprintf("module_voltage=%.2fV", v)},
		}}
	}
	return nil
}

func coOccurrenceFindings(focus string, stats []CoOccurStat) []Finding {
	if focus == "" || len(stats) == 0 {
		return nil
	}
	var top []string
	var evidence []string
	for i, s := range stats {
		if i >= 3 {
			break
		}
		if s.Rate < 0.25 || s.WithCount < 2 {
			continue
		}
		top = append(top, s.Code)
		evidence = append(evidence, fmt.Sprintf("%s appeared in %.0f%% of scans that also had %s (n=%d)",
			s.Code, s.Rate*100, focus, s.WithCount))
	}
	if len(top) == 0 {
		return nil
	}
	return []Finding{{
		ID:         "fleet.cooccurrence",
		Title:      "Network co-occurrence patterns",
		Summary:    "Across your shop network, this code often appears with: " + strings.Join(top, ", ") + ". Use as prior — not proof.",
		Confidence: 58,
		Severity:   "info",
		RelatedDTCs: append([]string{focus}, top...),
		Evidence:   evidence,
	}}
}

func hasAny(codes map[string]obd.DTC, list ...string) bool {
	for _, c := range list {
		if _, ok := codes[strings.ToUpper(c)]; ok {
			return true
		}
	}
	return false
}

func related(codes map[string]obd.DTC, list ...string) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		c = strings.ToUpper(c)
		if _, ok := codes[c]; ok {
			out = append(out, c)
		}
	}
	return out
}

func asFloat(v any) *float64 {
	switch t := v.(type) {
	case float64:
		return &t
	case float32:
		f := float64(t)
		return &f
	case int:
		f := float64(t)
		return &f
	case int64:
		f := float64(t)
		return &f
	case string:
		var f float64
		_, err := fmt.Sscanf(t, "%f", &f)
		if err == nil {
			return &f
		}
	}
	return nil
}

func rank(in []Finding) []Finding {
	// simple insertion by confidence desc
	out := make([]Finding, len(in))
	copy(out, in)
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1].Confidence < out[j].Confidence {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}
