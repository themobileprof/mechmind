package obd

import (
	"fmt"
	"io"
	"testing"
	"time"
)

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

func TestCommandWaitATZ(t *testing.T) {
	if commandWait("ATZ") < 2*time.Second {
		t.Fatal("ATZ needs a longer window than ATI")
	}
	if commandWait("ATI") >= commandWait("ATZ") {
		t.Fatal("ATI should stay short")
	}
}

func TestIsPortDead(t *testing.T) {
	if isPortDead(nil) {
		t.Fatal("nil")
	}
	if !isPortDead(io.EOF) {
		t.Fatal("eof")
	}
	if !isPortDead(fmt.Errorf("Port has been closed")) {
		t.Fatal("closed")
	}
}

func TestPID0100Supported(t *testing.T) {
	if !pid0100Supported("4100BE1FA813") {
		t.Fatal("compact 41 00")
	}
	if !pid0100Supported("41 00 BE 1F A8 13") {
		t.Fatal("spaced 41 00")
	}
	if pid0100Supported("SEARCHING...\nUNABLE TO CONNECT") {
		t.Fatal("unable")
	}
	if pid0100Supported("") || pid0100Supported("NO DATA") {
		t.Fatal("empty")
	}
}

func TestELMBusProtocolsCANFirst(t *testing.T) {
	if elmBusProtocols[0].Code != "6" {
		t.Fatalf("want CAN 11/500 first, got %+v", elmBusProtocols[0])
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
