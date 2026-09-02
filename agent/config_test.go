package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDXLogRequiresPort(t *testing.T) {
	path := writeTempConfig(t, `{
		"source": "dxlog",
		"station_id": "s1",
		"radios": [{"id": "r1", "radio_nr": 1}]
	}`)
	_, err := loadConfig(path, "")
	if err == nil {
		t.Fatal("expected error for missing dxlog_port, got nil")
	}
}

func TestLoadConfigDXLogValid(t *testing.T) {
	path := writeTempConfig(t, `{
		"source": "dxlog",
		"station_id": "s1",
		"dxlog_port": 13063,
		"default_operator": "WT2P",
		"radios": [{"id": "r1", "label": "Radio 1", "radio_nr": 1}]
	}`)
	cfg, err := loadConfig(path, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DXLogPort != 13063 {
		t.Fatalf("DXLogPort: got %d, want 13063", cfg.DXLogPort)
	}
	if cfg.Radios[0].RadioNr != 1 {
		t.Fatalf("radio_nr not parsed: got %d, want 1", cfg.Radios[0].RadioNr)
	}
}

func TestLoadConfigDXLogRadioMissingRadioNr(t *testing.T) {
	path := writeTempConfig(t, `{
		"source": "dxlog",
		"dxlog_port": 13063,
		"radios": [{"id": "r1"}]
	}`)
	_, err := loadConfig(path, "")
	if err == nil {
		t.Fatal("expected error for missing radio_nr, got nil")
	}
}

func TestLoadConfigSourceFlagOverridesFile(t *testing.T) {
	path := writeTempConfig(t, `{
		"n1mm_port": 12060,
		"radios": [{"id": "r1", "radio_nr": 1}]
	}`)
	// No "source" in the file at all -- must come from the flag.
	cfg, err := loadConfig(path, "n1mm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Source != "n1mm" {
		t.Fatalf("Source: got %q, want n1mm (from flag)", cfg.Source)
	}
}

func TestLoadConfigNoSourceNoFlagErrors(t *testing.T) {
	path := writeTempConfig(t, `{"station_id": "s1"}`)
	_, err := loadConfig(path, "")
	if err == nil {
		t.Fatal("expected error when no source is configured anywhere")
	}
}

func TestLoadConfigTCIMissingPort(t *testing.T) {
	path := writeTempConfig(t, `{
		"source": "tci",
		"radios": [{"id": "r1", "tci_host": "127.0.0.1"}]
	}`)
	_, err := loadConfig(path, "")
	if err == nil {
		t.Fatal("expected error for missing tci_port, got nil")
	}
}

func TestLoadConfigTCIMissingHost(t *testing.T) {
	path := writeTempConfig(t, `{
		"source": "tci",
		"radios": [{"id": "r1", "tci_port": 40001}]
	}`)
	_, err := loadConfig(path, "")
	if err == nil {
		t.Fatal("expected error for missing tci_host, got nil")
	}
}

func TestLoadConfigTCIValid(t *testing.T) {
	path := writeTempConfig(t, `{
		"source": "tci",
		"radios": [{"id": "r1", "label": "Panadapter 0", "tci_host": "127.0.0.1", "tci_port": 40001, "tci_receiver": 0}]
	}`)
	cfg, err := loadConfig(path, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Radios[0].TCIPort != 40001 {
		t.Fatalf("TCIPort: got %d, want 40001", cfg.Radios[0].TCIPort)
	}
}

func TestParseFlagsSourceFlag(t *testing.T) {
	configPath, source, err := parseFlags([]string{"--source", "dxlog", "config.json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "dxlog" {
		t.Fatalf("source: got %q, want dxlog", source)
	}
	if configPath != "config.json" {
		t.Fatalf("configPath: got %q, want config.json", configPath)
	}
}
