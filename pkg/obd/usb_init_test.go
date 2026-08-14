package obd

import "testing"

func TestLooksLikeELM(t *testing.T) {
	ok := []string{"ELM327 v1.5", "elm327", "STN1110", "OBDII to RS232 Interpreter", "OK"}
	for _, s := range ok {
		if !looksLikeELM(s) {
			t.Fatalf("expected ELM for %q", s)
		}
	}
	if looksLikeELM("") || looksLikeELM("UNABLE TO CONNECT") {
		t.Fatal("false positive")
	}
}

func TestBaudCandidatesPrefersRequested(t *testing.T) {
	u := NewUSBAdapter("/dev/ttyACM0")
	u.Baud = 9600
	got := u.baudCandidates()
	if len(got) < 2 || got[0] != 9600 {
		t.Fatalf("got %v", got)
	}
}
