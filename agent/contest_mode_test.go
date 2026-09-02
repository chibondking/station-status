package main

import (
	"strconv"
	"testing"
)

// These tests are the ones that actually prove the contest_mode privacy
// property: with it on, the outbound radioReport carries a band name and
// NO exact frequency; with it off (or unset), behavior is byte-for-byte
// what it is today. Both real code paths (TCI and N1MM/DXLog) are
// exercised via the extracted *RadiosSnapshot functions, populated the
// same way the live sources populate themselves -- not a reimplementation
// of the mapping.

const contestTestFreqHz = 14_074_000 // 20m FT8, a real operating frequency

func tciSourceWithFreq(t *testing.T, host string, port, receiver int, hz int64) *TCISource {
	t.Helper()
	src := NewTCISource(host, port)
	// vfo:<receiver>,<vfo_index>,<freq_hz>; -- the real wire format, see tci.go
	src.apply("vfo", []string{strconv.Itoa(receiver), "0", strconv.FormatInt(hz, 10)})
	return src
}

func TestContestModeTCIMasksFrequency(t *testing.T) {
	cfg := &Config{
		ContestMode: true,
		Radios: []RadioConfig{
			{ID: "r1", Label: "Radio 1", TCIHost: "127.0.0.1", TCIPort: 40001, TCIReceiver: 0},
		},
	}
	sources := map[string]*TCISource{
		tciSourceKey("127.0.0.1", 40001): tciSourceWithFreq(t, "127.0.0.1", 40001, 0, contestTestFreqHz),
	}

	reports := tciRadiosSnapshot(cfg, sources)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	r := reports[0]

	if r.FreqHz != nil {
		t.Fatalf("contest_mode on: FreqHz must be nil in the outbound report, got %d", *r.FreqHz)
	}
	if r.Band == nil || *r.Band != "20M" {
		t.Fatalf("contest_mode on: Band must be %q, got %v", "20M", r.Band)
	}
}

func TestContestModeOffTCIUnchanged(t *testing.T) {
	cfg := &Config{
		ContestMode: false, // the default; also covers "omitted from config"
		Radios: []RadioConfig{
			{ID: "r1", Label: "Radio 1", TCIHost: "127.0.0.1", TCIPort: 40001, TCIReceiver: 0},
		},
	}
	sources := map[string]*TCISource{
		tciSourceKey("127.0.0.1", 40001): tciSourceWithFreq(t, "127.0.0.1", 40001, 0, contestTestFreqHz),
	}

	r := tciRadiosSnapshot(cfg, sources)[0]

	if r.Band != nil {
		t.Fatalf("contest_mode off: Band must stay nil, got %q", *r.Band)
	}
	if r.FreqHz == nil || *r.FreqHz != contestTestFreqHz {
		t.Fatalf("contest_mode off: FreqHz must be sent as normal (%d), got %v", contestTestFreqHz, r.FreqHz)
	}
}

func TestContestModeN1MMMasksFrequency(t *testing.T) {
	cfg := &Config{
		ContestMode: true,
		Radios:      []RadioConfig{{ID: "r1", Label: "Radio 1", RadioNr: 1}},
	}
	src := NewN1MMSource(12060, "WT2P", "n1mm")
	// Freq is tens-of-Hz on the wire (see n1mm.go), so send /10.
	src.apply([]byte(`<RadioInfo><RadioNr>1</RadioNr><Freq>1407400</Freq><Mode>USB</Mode><OpCall>WT2P</OpCall></RadioInfo>`))

	r := n1mmRadiosSnapshot(cfg, src, "n1mm")[0]

	if r.FreqHz != nil {
		t.Fatalf("contest_mode on: FreqHz must be nil, got %d", *r.FreqHz)
	}
	if r.Band == nil || *r.Band != "20M" {
		t.Fatalf("contest_mode on: Band must be %q, got %v", "20M", r.Band)
	}
	// Explicitly out of scope: mode/operator are untouched by contest_mode.
	if r.Mode == nil || *r.Mode != "USB" {
		t.Fatalf("contest_mode must not affect Mode: got %v", r.Mode)
	}
	if r.Operator == nil || *r.Operator != "WT2P" {
		t.Fatalf("contest_mode must not affect Operator: got %v", r.Operator)
	}
}

func TestContestModeOffN1MMUnchanged(t *testing.T) {
	cfg := &Config{
		Radios: []RadioConfig{{ID: "r1", Label: "Radio 1", RadioNr: 1}},
	}
	src := NewN1MMSource(12060, "WT2P", "n1mm")
	src.apply([]byte(`<RadioInfo><RadioNr>1</RadioNr><Freq>1407400</Freq><Mode>USB</Mode><OpCall>WT2P</OpCall></RadioInfo>`))

	r := n1mmRadiosSnapshot(cfg, src, "n1mm")[0]

	if r.Band != nil {
		t.Fatalf("contest_mode off: Band must stay nil, got %q", *r.Band)
	}
	if r.FreqHz == nil || *r.FreqHz != contestTestFreqHz {
		t.Fatalf("contest_mode off: FreqHz must be sent as normal (%d), got %v", contestTestFreqHz, r.FreqHz)
	}
}

// TestContestModeLoadFromConfig confirms the field parses from JSON with
// no special validation, and that omitting it defaults to false.
func TestContestModeLoadFromConfig(t *testing.T) {
	on := writeTempConfig(t, `{
		"source": "tci",
		"contest_mode": true,
		"radios": [{"id": "r1", "tci_host": "127.0.0.1", "tci_port": 40001, "tci_receiver": 0}]
	}`)
	cfg, err := loadConfig(on, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ContestMode {
		t.Fatal("contest_mode: true in config should parse as true")
	}

	off := writeTempConfig(t, `{
		"source": "tci",
		"radios": [{"id": "r1", "tci_host": "127.0.0.1", "tci_port": 40001, "tci_receiver": 0}]
	}`)
	cfg2, err := loadConfig(off, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg2.ContestMode {
		t.Fatal("contest_mode omitted should default to false")
	}
}
