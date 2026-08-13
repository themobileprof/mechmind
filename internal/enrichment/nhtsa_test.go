package enrichment

import (
	"context"
	"testing"

	"github.com/autoservice/autoservice/internal/market"
)

func TestMarketScope(t *testing.T) {
	if !market.InMarketScope("Toyota", 2015) {
		t.Fatal("expected Toyota 2015 in scope")
	}
	if market.InMarketScope("Toyota", 2008) {
		t.Fatal("expected pre-2010 out of scope")
	}
	if market.InMarketScope("Ford", 2018) {
		t.Fatal("expected Ford out of NG top-5 scope")
	}
}

func TestLeanDecodeLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network")
	}
	c := NewClient()
	lean, err := c.DecodeVIN(context.Background(), "4T1BF1FK5FU628918")
	if err != nil {
		t.Skipf("nhtsa unavailable: %v", err)
	}
	if lean.Make == "" || lean.Year == 0 {
		t.Fatalf("incomplete lean decode: %+v", lean)
	}
	t.Logf("lean: %s %s %d %s", lean.Make, lean.Model, lean.Year, lean.FuelType)
}
