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

	freq0, mode0, _ := src.GetReceiver(0)
	if freq0 == nil || *freq0 != 14080500 {
		t.Fatalf("receiver 0 freq: got %v, want 14080500", freq0)
	}
	if mode0 == nil || *mode0 != "USB" {
		t.Fatalf("receiver 0 mode: got %v, want USB", mode0)
	}

	freq1, mode1, _ := src.GetReceiver(1)
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

	freq0, mode0, _ := src.GetReceiver(0)
	if freq0 == nil || *freq0 != 3688700 {
		t.Fatalf("receiver 0 freq: got %v, want 3688700", freq0)
	}
	// The real capture ends with modulation:0,cw -- despite arriving via
	// the comma-joined artifact line. If this fails, the comma-stripping
	// fix regressed.
	if mode0 == nil || *mode0 != "CW" {
		t.Fatalf("receiver 0 mode: got %v, want CW (normalized uppercase from 'cw')", mode0)
	}

	freq1, _, _ := src.GetReceiver(1)
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

// --- Transmit state (trx:) ------------------------------------------------
//
// These cover the per-receiver TX tracking behind the page's [TX] chip.
// Note that unlike the capture test above, the trx: command name itself
// has not been verified against real AetherSDR output -- see tci.go's
// header. What these prove is that IF the stream says trx:, we track it
// per receiver, we don't get stuck on, and we don't guess.

func TestTrxPerReceiver(t *testing.T) {
	src := NewTCISource("127.0.0.1", 40001)
	// Both slices tuned up, then only slice 1 keys.
	for _, line := range []string{
		"vfo:0,0,14074000;",
		"vfo:1,0,7074000;",
		"trx:1,true;",
	} {
		for _, p := range parseTCIMessage(line) {
			src.apply(p[0].(string), p[1].([]string))
		}
	}

	if _, _, tx0 := src.GetReceiver(0); tx0 {
		t.Fatal("receiver 0 must not read as transmitting: only receiver 1 was keyed")
	}
	if _, _, tx1 := src.GetReceiver(1); !tx1 {
		t.Fatal("receiver 1 must read as transmitting after trx:1,true")
	}

	// Unkey.
	for _, p := range parseTCIMessage("trx:1,false;") {
		src.apply(p[0].(string), p[1].([]string))
	}
	if _, _, tx1 := src.GetReceiver(1); tx1 {
		t.Fatal("receiver 1 must stop reading as transmitting after trx:1,false")
	}
}

// The optional third field says what keyed the radio ("mic", "vox",
// "tci"). It must not change whether we read it as transmitting.
func TestTrxIgnoresSignalSource(t *testing.T) {
	src := NewTCISource("127.0.0.1", 40001)
	for _, p := range parseTCIMessage("trx:0,true,mic;") {
		src.apply(p[0].(string), p[1].([]string))
	}
	if _, _, tx := src.GetReceiver(0); !tx {
		t.Fatal("trx:0,true,mic must read as transmitting")
	}
}

// A value we can't parse leaves the previous state alone rather than
// defaulting to false: dropping the chip mid-transmission is the one
// direction that actively misleads an operator reading the page.
func TestTrxUnparseableKeepsPreviousState(t *testing.T) {
	src := NewTCISource("127.0.0.1", 40001)
	for _, line := range []string{"trx:0,true;", "trx:0,maybe;", "trx:0;"} {
		for _, p := range parseTCIMessage(line) {
			src.apply(p[0].(string), p[1].([]string))
		}
	}
	if _, _, tx := src.GetReceiver(0); !tx {
		t.Fatal("garbage trx: value must not clear a known-true transmit state")
	}
}

// The stuck-chip guard: TCI only ever tells us TX ended by sending
// trx:<rx>,false, so losing the socket mid-transmission has to clear it
// here or the page shows a radio keyed forever.
func TestDisconnectClearsTransmitState(t *testing.T) {
	src := NewTCISource("127.0.0.1", 40001)
	for _, p := range parseTCIMessage("vfo:0,0,14074000;trx:0,true;") {
		src.apply(p[0].(string), p[1].([]string))
	}
	src.setConnected(true)
	if _, _, tx := src.GetReceiver(0); !tx {
		t.Fatal("precondition: receiver 0 should be transmitting")
	}

	src.setConnected(false)

	freq, _, tx := src.GetReceiver(0)
	if tx {
		t.Fatal("transmit state must be cleared when the TCI connection drops")
	}
	// Frequency is deliberately NOT cleared -- it's still the last place
	// that radio was, and the server blanks it once the radio reads offline.
	if freq == nil || *freq != 14074000 {
		t.Fatalf("frequency must survive a disconnect, got %v", freq)
	}
}
