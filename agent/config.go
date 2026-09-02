package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type RadioConfig struct {
	ID    string `json:"id"`
	Label string `json:"label"`

	// TCI mode fields
	TCIHost     string `json:"tci_host"`
	TCIPort     int    `json:"tci_port"`
	TCIReceiver int    `json:"tci_receiver"`

	// N1MM / DXLog (N1MM-compatible mode) fields -- RadioNr is 1-based,
	// matching N1MM's own convention (RadioNr 1 / RadioNr 2), not our
	// 0-based TCI receiver numbering. Deliberately not renumbered, to
	// avoid a second silent off-by-one after the panadapter-numbering one
	// we already hit once. Shared field name because both sources speak
	// the same wire format -- see n1mm.go's header comment.
	RadioNr int `json:"radio_nr"`
}

type Config struct {
	// Source selects the operating mode for this whole agent process:
	// "tci", "n1mm", or "dxlog" (DXLog configured in its N1MM-compatible
	// broadcast mode -- see n1mm.go for why that reuses the same parser).
	// Can also be set via the --source CLI flag, which overrides whatever
	// is in the file.
	Source                string        `json:"source"`
	// ContestMode is a whole-agent-process privacy setting (like Source,
	// not per-radio). When true, the agent computes a band name from the
	// real operating frequency and sends only that to the server -- the
	// exact frequency never leaves the shack LAN. Default false / omitted.
	// See band.go for the band edges and their source.
	ContestMode           bool          `json:"contest_mode"`
	StationID             string        `json:"station_id"`
	StationName           string        `json:"station_name"`
	ServerURL             string        `json:"server_url"`
	APIToken              string        `json:"api_token"`
	ReportIntervalSeconds float64       `json:"report_interval_seconds"`
	Radios                []RadioConfig `json:"radios"`

	// N1MM-mode only. Default is commonly 12060 but confirm against
	// N1MM's own Broadcast Data tab.
	N1MMPort int `json:"n1mm_port"`
	// DXLog-mode only (DXLog configured for N1MM-style radio broadcast).
	// DXLog's own documented default for this feature is 13063 -- a
	// different default than N1MM's, confirmed via DXLog's docs, not
	// carried over by assumption.
	DXLogPort int `json:"dxlog_port"`
	// Used when OpCall and mycall are both empty (shouldn't normally
	// happen -- N1MM/DXLog default OpCall to the station callsign itself
	// -- but this is a defensive fallback, not a guess dressed up as fact).
	DefaultOperator string `json:"default_operator"`
}

func loadConfig(path string, sourceFlag string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if sourceFlag != "" {
		cfg.Source = sourceFlag
	}

	switch cfg.Source {
	case "tci":
		if err := validateTCIRadios(cfg.Radios); err != nil {
			return nil, err
		}
	case "n1mm":
		if cfg.N1MMPort == 0 {
			return nil, fmt.Errorf("source=n1mm requires n1mm_port in config (check N1MM's Broadcast Data tab for the configured port -- commonly 12060, but don't assume)")
		}
		if err := validateRadioNrs(cfg.Radios, "n1mm"); err != nil {
			return nil, err
		}
	case "dxlog":
		if cfg.DXLogPort == 0 {
			return nil, fmt.Errorf("source=dxlog requires dxlog_port in config (check DXLog's network config panel for the Radio Information broadcast port -- documented default is 13063, different from N1MM's 12060; confirm rather than assume it wasn't changed)")
		}
		if err := validateRadioNrs(cfg.Radios, "dxlog"); err != nil {
			return nil, err
		}
	case "":
		return nil, fmt.Errorf("no source configured -- set \"source\" in config.json or pass --source (tci|n1mm|dxlog)")
	default:
		return nil, fmt.Errorf("unknown source %q (supported: tci, n1mm, dxlog)", cfg.Source)
	}

	if cfg.ReportIntervalSeconds <= 0 {
		cfg.ReportIntervalSeconds = 5
	}
	return &cfg, nil
}

func validateRadioNrs(radios []RadioConfig, source string) error {
	for _, r := range radios {
		if r.RadioNr == 0 {
			return fmt.Errorf("radio %q: source=%s requires radio_nr (1 or 2, matching %s's own RadioNr)", r.ID, source, source)
		}
	}
	return nil
}

// validateTCIRadios exists for the same reason validateRadioNrs does: an
// unset port shouldn't fail quietly as an endless "connection refused"
// retry loop -- it should fail loudly, once, at startup, with a message
// that says what to check. tci_port has no sane default to fall back to
// (AetherSDR's port is configured per-install, not fixed), so this is a
// required field, not a "commonly X" suggestion.
func validateTCIRadios(radios []RadioConfig) error {
	for _, r := range radios {
		if r.TCIHost == "" {
			return fmt.Errorf("radio %q: source=tci requires tci_host (e.g. \"127.0.0.1\" if AetherSDR runs on this same machine)", r.ID)
		}
		if r.TCIPort == 0 {
			return fmt.Errorf("radio %q: source=tci requires tci_port -- check AetherSDR's own TCI panel for the configured port, don't assume a default", r.ID)
		}
	}
	return nil
}

// parseFlags is separated from main() so it's testable without touching
// os.Args/flag's global state in every test.
func parseFlags(args []string) (configPath string, source string, err error) {
	fs := flag.NewFlagSet("stationagent", flag.ContinueOnError)
	fs.StringVar(&source, "source", "", "override config.json's \"source\" field: tci | n1mm | dxlog")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 1 {
		return "", "", fmt.Errorf("usage: stationagent [--source tci|n1mm|dxlog] config.json")
	}
	return fs.Arg(0), source, nil
}
