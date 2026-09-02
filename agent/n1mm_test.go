package main

import (
	"testing"
	"time"
)

// The exact example packet from N1MM's official documentation
// (https://n1mmwp.hamdocs.com/appendices/external-udp-broadcasts/).
const n1mmExamplePacket = `<?xml version="1.0" encoding="utf-8"?>
<RadioInfo>
<app>N1MM</app>
<StationName>CW-80m</StationName>
<RadioNr>1</RadioNr>
<Freq>352211</Freq>
<TXFreq>352211</TXFreq>
<Mode>CW</Mode>
<mycall>W1ABC</mycall>
<OpCall>W1ABC</OpCall>
<IsRunning>False</IsRunning>
<FocusEntry>204626</FocusEntry>
<EntryWindowHwnd>275678</EntryWindowHwnd>
<Antenna>8</Antenna>
<Rotors></Rotors>
<FocusRadioNr>1</FocusRadioNr>
<IsStereo>False</IsStereo>
<IsSplit>False</IsSplit>
<ActiveRadioNr>1</ActiveRadioNr>
<IsTransmitting>False</IsTransmitting>
<FunctionKeyCaption></FunctionKeyCaption>
<RadioName></RadioName>
<AuxAntSelected>-1</AuxAntSelected>
<AuxAntSelectedName></AuxAntSelectedName>
<IsConnected></IsConnected>
</RadioInfo>`

func TestN1MMOfficialExamplePacket(t *testing.T) {
	src := NewN1MMSource(12060, "WT2P", "n1mm")
	src.apply([]byte(n1mmExamplePacket))

	freq, mode, operator, connected := src.GetRadio(1)
	if !connected {
		t.Fatal("expected connected=true right after receiving a packet")
	}
	// 352211 * 10 -- confirmed against the example's own "CW-80m" label:
	// 3,522,110 Hz = 3.52211 MHz, in the 80m band.
	if freq == nil || *freq != 3522110 {
		t.Fatalf("freq: got %v, want 3522110 (352211 * 10)", freq)
	}
	if mode == nil || *mode != "CW" {
		t.Fatalf("mode: got %v, want CW", mode)
	}
	if operator == nil || *operator != "W1ABC" {
		t.Fatalf("operator: got %v, want W1ABC (from OpCall)", operator)
	}

	// Radio 2 was never reported -- must not be "connected".
	_, _, _, connected2 := src.GetRadio(2)
	if connected2 {
		t.Fatal("radio 2 was never reported, should not read as connected")
	}
}

func TestN1MMOperatorFallsBackToMyCallThenDefault(t *testing.T) {
	// OpCall blank -- should fall back to mycall.
	src := NewN1MMSource(12060, "WT2P", "n1mm")
	packet := `<RadioInfo><RadioNr>1</RadioNr><Freq>140805</Freq><Mode>usb</Mode><mycall>WT2P</mycall><OpCall></OpCall></RadioInfo>`
	src.apply([]byte(packet))
	_, mode, operator, _ := src.GetRadio(1)
	if operator == nil || *operator != "WT2P" {
		t.Fatalf("operator: got %v, want WT2P (fallback to mycall)", operator)
	}
	// Also confirms mode normalizes to uppercase even though the input
	// here is lowercase (real N1MM sends uppercase per docs, but the
	// TCI lesson was: verify, don't assume vendors are consistent).
	if mode == nil || *mode != "USB" {
		t.Fatalf("mode: got %v, want USB", mode)
	}

	// Both OpCall and mycall blank -- should fall back to configured default.
	src2 := NewN1MMSource(12060, "WT2P", "n1mm")
	packet2 := `<RadioInfo><RadioNr>1</RadioNr><Freq>140805</Freq><Mode>CW</Mode><mycall></mycall><OpCall></OpCall></RadioInfo>`
	src2.apply([]byte(packet2))
	_, _, operator2, _ := src2.GetRadio(1)
	if operator2 == nil || *operator2 != "WT2P" {
		t.Fatalf("operator: got %v, want WT2P (configured default)", operator2)
	}
}

func TestN1MMIgnoresNonRadioInfoPackets(t *testing.T) {
	src := NewN1MMSource(12060, "WT2P", "n1mm")
	// A ContactInfo-shaped packet, which N1MM also sends on the same
	// port if enabled -- must not crash or be mistaken for RadioInfo.
	contactInfo := `<?xml version="1.0" encoding="utf-8"?><contactinfo><timestamp>2020-01-17 16:43:38</timestamp><call>K1XM</call></contactinfo>`
	src.apply([]byte(contactInfo))

	_, _, _, connected := src.GetRadio(1)
	if connected {
		t.Fatal("a ContactInfo packet should not register as radio state")
	}

	// Garbage should also not panic.
	src.apply([]byte("not xml at all"))
	src.apply([]byte(""))
}

func TestN1MMStaleAfterTimeout(t *testing.T) {
	src := NewN1MMSource(12060, "WT2P", "n1mm")
	src.apply([]byte(n1mmExamplePacket))
	// Force the recorded time backwards to simulate a stale radio,
	// rather than sleeping n1mmStaleAfter in a test.
	src.mu.Lock()
	src.radios[1].LastUpdate = time.Now().Add(-1 * time.Minute)
	src.mu.Unlock()

	_, _, _, connected := src.GetRadio(1)
	if connected {
		t.Fatal("radio not updated in over a minute should be stale/disconnected")
	}
}
