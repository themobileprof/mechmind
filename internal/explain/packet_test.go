package explain

import "testing"

func TestWorthNarrating(t *testing.T) {
	p := Packet{Code: "P0171"}
	ok, reason := WorthNarrating(p, false)
	if ok || reason != "no_findings_or_kb" {
		t.Fatalf("got %v %s", ok, reason)
	}
	p.KB = &PacketKB{Title: "Lean", Summary: "x"}
	ok, reason = WorthNarrating(p, false)
	if ok || reason != "kb_only" {
		t.Fatalf("got %v %s", ok, reason)
	}
	p.Findings = []PacketFinding{{ID: "a", Title: "t", Confidence: 90}}
	ok, reason = WorthNarrating(p, false)
	if ok || reason != "structured_sufficient" {
		t.Fatalf("got %v %s", ok, reason)
	}
	p.Findings = []PacketFinding{
		{ID: "a", Confidence: 70},
		{ID: "b", Confidence: 60},
	}
	ok, reason = WorthNarrating(p, false)
	if !ok || reason != "ok" {
		t.Fatalf("got %v %s", ok, reason)
	}
	ok, _ = WorthNarrating(p, true)
	if !ok {
		t.Fatal("force should narrate")
	}
}

func TestFingerprintStable(t *testing.T) {
	p := Packet{Code: "P0171", Ask: "x", Findings: []PacketFinding{{ID: "a", Title: "t", Confidence: 1}}}
	a, b := p.Fingerprint(), p.Fingerprint()
	if a != b || a == "" {
		t.Fatalf("fingerprint unstable %s %s", a, b)
	}
}
