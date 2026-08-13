package obd

import "testing"

func TestDecodeDTCs(t *testing.T) {
	codes := DecodeDTCs("43 01 33 01 71")
	if len(codes) != 2 {
		t.Fatalf("expected 2 codes, got %v", codes)
	}
	if codes[0] != "P0133" {
		t.Fatalf("got %s want P0133", codes[0])
	}
	if codes[1] != "P0171" {
		t.Fatalf("got %s want P0171", codes[1])
	}
}

func TestDescribeCode(t *testing.T) {
	if got := DescribeCode("p0300"); got == "" || got == "Powertrain diagnostic trouble code" {
		t.Fatalf("expected specific description, got %q", got)
	}
}

func TestMockScanner(t *testing.T) {
	s := &Scanner{Adapter: NewMockAdapter("MOCKTESTVIN000001", []string{"P0300", "P0420"})}
	res, err := s.Scan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.LinkType != LinkMock {
		t.Fatalf("link %s", res.LinkType)
	}
	if res.Vehicle.VIN != "MOCKTESTVIN000001" {
		t.Fatalf("vin %s", res.Vehicle.VIN)
	}
	if len(res.DTCs) != 2 {
		t.Fatalf("dtcs %d", len(res.DTCs))
	}
	if res.DTCs[0].FreezeFrame == nil {
		t.Fatal("expected freeze frame")
	}
}
