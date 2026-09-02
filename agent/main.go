package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type radioReport struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	FreqHz    *int64  `json:"freq_hz"`
	Mode      *string `json:"mode"`
	Operator  *string `json:"operator"`
	Connected bool    `json:"connected"`
	Source    string  `json:"source"`
}

type reportPayload struct {
	StationID   string        `json:"station_id"`
	StationName string        `json:"station_name"`
	Radios      []radioReport `json:"radios"`
}

func tciSourceKey(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// buildSnapshot is a function value chosen once at startup based on
// cfg.Source, so the report loop itself doesn't need to branch on source
// type every tick.
type snapshotFunc func() []radioReport

func buildTCISnapshot(cfg *Config) snapshotFunc {
	sources := make(map[string]*TCISource)
	for _, r := range cfg.Radios {
		key := tciSourceKey(r.TCIHost, r.TCIPort)
		if _, ok := sources[key]; !ok {
			src := NewTCISource(r.TCIHost, r.TCIPort)
			sources[key] = src
			go src.Run()
		}
	}

	return func() []radioReport {
		var radios []radioReport
		for _, r := range cfg.Radios {
			key := tciSourceKey(r.TCIHost, r.TCIPort)
			src := sources[key]
			freq, mode := src.GetReceiver(r.TCIReceiver)
			radios = append(radios, radioReport{
				ID:        r.ID,
				Label:     r.Label,
				FreqHz:    freq,
				Mode:      mode,
				Operator:  nil, // not available from TCI
				Connected: src.IsConnected(),
				Source:    "tci",
			})
		}
		return radios
	}
}

func buildN1MMSnapshot(cfg *Config) snapshotFunc {
	return buildN1MMStyleSnapshot(cfg, cfg.N1MMPort, "n1mm")
}

func buildDXLogSnapshot(cfg *Config) snapshotFunc {
	// Reuses N1MMSource -- DXLog's N1MM-compatible broadcast mode speaks
	// the same wire format. See n1mm.go's header comment for the caveat
	// on how confident that assumption is.
	return buildN1MMStyleSnapshot(cfg, cfg.DXLogPort, "dxlog")
}

func buildN1MMStyleSnapshot(cfg *Config, port int, sourceLabel string) snapshotFunc {
	src := NewN1MMSource(port, cfg.DefaultOperator, sourceLabel)
	go src.Run()

	return func() []radioReport {
		var radios []radioReport
		for _, r := range cfg.Radios {
			freq, mode, operator, connected := src.GetRadio(r.RadioNr)
			radios = append(radios, radioReport{
				ID:        r.ID,
				Label:     r.Label,
				FreqHz:    freq,
				Mode:      mode,
				Operator:  operator,
				Connected: connected,
				Source:    sourceLabel,
			})
		}
		return radios
	}
}

func main() {
	configPath, sourceFlag, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	cfg, err := loadConfig(configPath, sourceFlag)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	var snapshot snapshotFunc
	switch cfg.Source {
	case "tci":
		snapshot = buildTCISnapshot(cfg)
	case "n1mm":
		snapshot = buildN1MMSnapshot(cfg)
	case "dxlog":
		snapshot = buildDXLogSnapshot(cfg)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	interval := time.Duration(cfg.ReportIntervalSeconds * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reportURL := strings.TrimRight(cfg.ServerURL, "/") + "/api/report"

	for range ticker.C {
		payload := reportPayload{
			StationID:   cfg.StationID,
			StationName: cfg.StationName,
			Radios:      snapshot(),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("marshal error: %v", err)
			continue
		}

		req, err := http.NewRequest("POST", reportURL, bytes.NewReader(body))
		if err != nil {
			log.Printf("request build error: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("report failed: %v", err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("report rejected: HTTP %d", resp.StatusCode)
		}
	}
}
