package obd

import (
	"strings"
	"testing"
)

func TestClassifyUSBIdentityOpenPort(t *testing.T) {
	if ClassifyUSBIdentity("0403", "cc4d", "", "") != KindJ2534OpenPort {
		t.Fatal("0403:cc4d should be OpenPort")
	}
	if ClassifyUSBIdentity("0403", "CC4D", "Clone", "OpenPort 2.0") != KindJ2534OpenPort {
		t.Fatal("product OpenPort should match")
	}
	if ClassifyUSBIdentity("0403", "6001", "FTDI", "USB Serial") != KindELMCandidate {
		t.Fatal("generic FTDI UART is not OpenPort")
	}
	if ClassifyUSBIdentity("1a86", "7523", "", "USB2.0-Serial") != KindELMCandidate {
		t.Fatal("CH340 ELM clone")
	}
	if ClassifyUSBIdentity("", "", "Tactrix", "Openport 2.0") != KindJ2534OpenPort {
		t.Fatal("name-only Tactrix")
	}
}

func TestSerialPortLabel(t *testing.T) {
	p := SerialPort{Path: "/dev/ttyACM0", Kind: KindJ2534OpenPort, VID: "0403", PID: "cc4d"}
	s := p.DisplayLabel()
	if !strings.Contains(s, "J2534") || !strings.Contains(s, "/dev/ttyACM0") {
		t.Fatalf("label %q", s)
	}
}
