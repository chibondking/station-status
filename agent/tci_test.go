package main

import "testing"

func TestParseAndApply(t *testing.T) {
	frame := "vfo:0,0,14080500;modulation:0,USB;vfo:1,0,7074000;modulation:1,USB;"
	pairs := parseTCIMessage(frame)
	if len(pairs) != 4 {
		t.Fatalf("expected 4 commands, got %d: %v", len(pairs), pairs)
	}

	src := NewTCISource("127.0.0.1", 40001)
	for _, p := range pairs {
		src.apply(p[0].(string), p[1].([]string))
	}

	freq0, mode0 := src.GetReceiver(0)
	if freq0 == nil || *freq0 != 14080500 {
		t.Fatalf("receiver 0 freq: got %v, want 14080500", freq0)
	}
	if mode0 == nil || *mode0 != "USB" {
		t.Fatalf("receiver 0 mode: got %v, want USB", mode0)
	}

	freq1, mode1 := src.GetReceiver(1)
	if freq1 == nil || *freq1 != 7074000 {
		t.Fatalf("receiver 1 freq: got %v, want 7074000", freq1)
	}
	if mode1 == nil || *mode1 != "USB" {
		t.Fatalf("receiver 1 mode: got %v, want USB", mode1)
	}
}

// TestRealAetherSDRCapture uses lines copy-pasted from an actual AetherSDR
// TCI session (websocat capture), including the stray-comma artifact that
// showed up joining two messages. This is a regression test against real
// hardware output, not just the documented spec.
func TestRealAetherSDRCapture(t *testing.T) {
	lines := []string{
		"rx_smeter:0,-91;",
		"vfo:1,0,1840000;",
		"dds:1,3865000;",
		"vfo:1,0,1840000;",
		"dds:1,1840000;",
		"vfo:1,0,1854500;",
		"dds:1,1840000;",
		"vfo:0,0,3689250;",
		"dds:0,3694284;",
		"active_slice:0,A;",
		"vfo:0,0,3688700;",
		"dds:0,3694284;",
		"modulation:0,usb;",
		"rx_filter_band:0,100,2200;",
		"rx_smeter:0,-92;",
		"rx_filter_band:0,100,2800;",
		"rx_smeter:0,-91;",
		// the actual artifact seen in the real capture: two messages
		// joined by a stray comma instead of a clean ';' boundary
		"rx_smeter:0,-91;,modulation:0,cw;",
		"rx_smeter:0,-91;",
		"sql_enable:0,true;",
		"rx_filter_band:0,-200,200;",
	}

	src := NewTCISource("127.0.0.1", 40001)
	for _, line := range lines {
		for _, p := range parseTCIMessage(line) {
			src.apply(p[0].(string), p[1].([]string))
		}
	}

	freq0, mode0 := src.GetReceiver(0)
	if freq0 == nil || *freq0 != 3688700 {
		t.Fatalf("receiver 0 freq: got %v, want 3688700", freq0)
	}
	// The real capture ends with modulation:0,cw -- despite arriving via
	// the comma-joined artifact line. If this fails, the comma-stripping
	// fix regressed.
	if mode0 == nil || *mode0 != "CW" {
		t.Fatalf("receiver 0 mode: got %v, want CW (normalized uppercase from 'cw')", mode0)
	}

	freq1, _ := src.GetReceiver(1)
	if freq1 == nil || *freq1 != 1854500 {
		t.Fatalf("receiver 1 freq: got %v, want 1854500", freq1)
	}
}

func TestMalformedFrameDoesNotPanic(t *testing.T) {
	src := NewTCISource("127.0.0.1", 40001)
	for _, p := range parseTCIMessage("vfo:garbage;modulation:0;garbage_no_colon;;") {
		src.apply(p[0].(string), p[1].([]string))
	}
	// just must not panic
}
