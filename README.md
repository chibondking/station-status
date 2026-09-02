# Station Status Page

A near-real-time station status page (K1TTT-style) built from:

- **`server/`** — one small persistent process on your VPS (Python/FastAPI).
  Agents POST status to it; the browser polls it; it serves the frontend.
- **`agent/`** — runs on each shack computer, **the one that connects
  outbound to your VPS** — nothing on the shack LAN is ever reached
  directly by the VPS. Written in Go and compiled to a single standalone
  binary — no Python, no runtime — for Windows and for Linux on both
  x86-64 and ARM (an ARM Chromebook's Linux/Crostini container included).
  Build the binaries with the commands in Setup, or publish them via a
  Release; source is the `.go` files if you ever need to rebuild.
  One agent process runs in exactly one mode, chosen via `"source"` in
  its config.json or the `--source` flag: `tci`, `n1mm`, or `dxlog`.
- **`server/static/index.html`** — the actual page, polls `/api/status`
  every 3s and renders a table per station, styled to match wt2p.us.

Everything has been **run and tested** in a sandbox — including the
actual compiled Go binaries talking to the actual Python server, and real
UDP packets for the N1MM path. See "What's been tested" at the bottom for
specifics on what's verified vs. what still needs checking on your
hardware.

## Why this design

- **Expandable**: a "station" is just a JSON object with an id, a name,
  and a list of radios. Adding a second computer/station later means
  running another agent with its own config — the server and frontend
  don't need code changes.
- **Offline detection has two layers**: the agent reports on a heartbeat
  (default every 5s) regardless of whether anything changed, and each
  report includes `connected: true/false`. If AetherSDR/N1MM isn't
  running, the agent reports `connected: false` and the radio goes
  Offline immediately. If the whole agent process dies, no reports arrive
  at all, and the server's staleness timeout (default 20s) catches that.
- **One source per agent process, chosen explicitly**: `config.go`
  requires a valid `"source"` (or `--source` flag) and refuses to start
  otherwise — no silent fallback, no guessing. N1MM/DXLog run on the shack
  LAN and get translated into the same authenticated HTTPS push the TCI
  path already uses; the VPS never talks UDP or trusts unauthenticated
  broadcast traffic directly.

## Setup

### Server (on your VPS)

```bash
cd server
python3 -m venv venv
./venv/bin/pip install fastapi uvicorn
STATUS_API_TOKEN=<pick-a-real-secret> STATUS_OFFLINE_THRESHOLD=20 ./venv/bin/python3 server.py
```

See `deploy/DEPLOY.md` for the full systemd + nginx + Cloudflare Tunnel
runbook.

Env vars:
- `STATUS_API_TOKEN` — shared secret agents must send. Change the default.
- `STATUS_OFFLINE_THRESHOLD` — seconds of silence before a station/radio
  is marked Offline. A few multiples of the agent's report interval
  (default 5s), so 15–20 is reasonable.
- `STATUS_HOST` / `STATUS_PORT` — default `0.0.0.0:8000`.

### Agent (on each shack computer, Windows or Linux — x86-64 or ARM)

Nothing to install — copy the binary for your OS/arch and a config file, then run it.

**TCI mode** (AetherSDR panadapters), using `config.example.tci.json`:
```
stationagent-windows-amd64.exe config.json
```
or with the flag instead of a `"source"` field in the file:
```
stationagent-windows-amd64.exe --source tci config.json
```

**N1MM mode**, using `config.example.n1mm.json`:
```
stationagent-windows-amd64.exe --source n1mm config.json
```

**DXLog mode** (configured for N1MM-compatible broadcast — see the DXLog
section below), using `config.example.dxlog.json`:
```
stationagent-windows-amd64.exe --source dxlog config.json
```

Pre-built binaries live in `agent/build/` (not committed). All the Linux
builds are fully static (no shared library dependencies, no libc version
to match).

**Where to get them:** the `agent-build` GitHub Actions workflow
(`.github/workflows/agent-build.yml`) builds all four targets — Windows
x86-64, Linux x86-64, Linux ARM64, Linux ARMv7 — on every branch push,
after `go vet` / `go test` pass. Each run attaches the binaries as
downloadable artifacts. Every merge to `main` additionally refreshes the
rolling **`agent-latest`** pre-release with the four binaries attached, so
"grab the current main build" is a one-click download from the Releases
page. Or build them yourself with the commands below.

**ARM Linux / Chromebook:** the agent is pure Go with no cgo, so it
cross-compiles to ARM with nothing more than a different `GOARCH`. On a
Chromebook this means running it inside the Linux (Crostini) container:
`stationagent-linux-arm64` for a modern ARM Chromebook (MediaTek Kompanio
/ Qualcomm 7c — the Crostini userland is 64-bit), `stationagent-linux-armv7`
for an older 32-bit one. Run it exactly like the amd64 Linux binary.

If you ever need to rebuild from source (e.g. after changing `agent/*.go`),
you need the Go toolchain on *some* machine — not necessarily the shack
computer:
```bash
cd agent
GOOS=windows GOARCH=amd64                 go build -o build/stationagent-windows-amd64.exe .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64     go build -o build/stationagent-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64     go build -o build/stationagent-linux-arm64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o build/stationagent-linux-armv7 .
```

## TCI format — confirmed against a real capture

Verified against an actual websocat capture of AetherSDR's TCI output
(not just docs), including forcing a mode change to see it broadcast live:

```
vfo:<receiver>,<vfo_index>,<frequency_hz>;   -- the operating frequency, in Hz
modulation:<receiver>,<mode>;                -- mode, sent LOWERCASE
                                                 ("usb", "cw") on connect
                                                 and on every change
```

`tci.go` normalizes mode to uppercase for consistent display, and handles
a real stray-comma artifact seen in the capture
(`rx_smeter:0,-91;,modulation:0,cw;`) — regression-tested in
`TestRealAetherSDRCapture` (`tci_test.go`) against the literal captured
data, artifact included.

Also present in real traffic, intentionally ignored: `dds:` (hardware/LO
tuning point — confirmed via the capture that it can sit fixed while
`vfo:` moves within the passband; `vfo:` is the one that tracks the
actual operating frequency), plus `rx_smeter:`, `rx_filter_band:`,
`active_slice:`, `sql_enable:`, and others.

**Confirmed:** receiver index maps directly to panadapter/RX number —
receiver 0 = Panadapter 0 = RX A, receiver 1 = Panadapter 1 = RX B, and so
on. `config.example.tci.json` uses this naming. Most of the time
(single-RX) you'll only need `panadapter0`; add more entries for SO2R and
beyond.

Still just a placeholder: the `40001` port in `config.example.tci.json`.
Check AetherSDR's own TCI panel for the real port. `tci_host`/`tci_port`
are required fields (`config_test.go`: `TestLoadConfigTCIMissingPort`,
`TestLoadConfigTCIMissingHost`) — an unset port fails immediately at
startup with a clear message, rather than silently retrying a connection
to port 0 forever. Same reasoning as the required `n1mm_port`/
`dxlog_port`: none of these three have a port worth hardcoding as a
default, since real setups (including yours) sometimes deviate from
common defaults.

Re-verify or capture more with `websocat ws://localhost:<your-tci-port>`
(single binary, no install) if anything changes.

## N1MM format — confirmed against official docs

Verified against N1MM Logger+'s official UDP broadcast documentation
(https://n1mmwp.hamdocs.com/appendices/external-udp-broadcasts/), which
includes a literal example `<RadioInfo>` packet — not inferred from a
description, the actual XML. `n1mm.go`'s header comment has the full
example and reasoning. Key points, including one genuine gotcha:

- Default port is **12060** — confirm against N1MM's own Broadcast Data
  tab, don't assume it wasn't changed.
- **`Freq`/`TXFreq` are in tens of Hz, not Hz.** The official example
  (`<Freq>352211</Freq>` labeled `<StationName>CW-80m</StationName>`)
  only checks out as 3.52211 MHz once multiplied by 10. `n1mm.go` does
  this multiplication; `TestN1MMOfficialExamplePacket` in `n1mm_test.go`
  regression-tests it against that literal example so it can't silently
  drift back to raw Hz.
- `RadioNr` is **1-based** (1 or 2) — deliberately not renumbered to
  0-based like TCI's receivers, so `config.example.n1mm.json` uses
  `n1mm_radio_nr: 1` / `2` directly, matching what you'd see in N1MM
  itself.
- `OpCall` is the operator; per the docs it defaults to the station's own
  callsign (`mycall`) when nobody has explicitly logged in via OPON.
  `n1mm.go` implements that same fallback chain — OpCall, then mycall,
  then the `default_operator` you set in config.json — as defense in
  depth, not as a guess about undocumented behavior.
- N1MM broadcasts several other packet types (ContactInfo, Spot, Score,
  etc.) on the same port if you enable them. Only `<RadioInfo>` is parsed;
  everything else fails to unmarshal (root element name won't match) and
  is silently ignored — confirmed intentional and tested
  (`TestN1MMIgnoresNonRadioInfoPackets`), not an oversight.
- Unlike TCI's live WebSocket, UDP broadcast has no "connection" to
  track — `n1mm.go` calls a radio Offline if no packet arrived in the
  last 25s (N1MM's own heartbeat is ~10s, so this allows a couple of
  missed beats before giving up).

This is real published spec, cross-referenced by several independent
third-party N1MM tools — solid enough to build against directly. Still,
if your actual N1MM output looks different from what's documented here
(version differences do happen), the fix is the same as it always is:
capture a real packet and tell me what's different, rather than assume
the doc is stale and patch around a guess.

## DXLog — reuses the N1MM parser, with one caveat

You mentioned you'll configure DXLog in its N1MM-compatible broadcast
mode (Options|Broadcast|Radio information, "N1MM-like format") —
something you'd specifically requested years ago for Node-RED dashboard
compatibility. Since that mode's whole purpose is to reproduce N1MM's own
`<RadioInfo>` schema, `dxlog.go`... doesn't exist. `source: "dxlog"`
reuses `n1mm.go`'s parser directly (`N1MMSource`), because it's the same
wire format, not a coincidentally-similar one.

What's different from plain N1MM mode, both confirmed via DXLog's own
docs:
- **Default port is 13063**, not N1MM's 12060 — a separate documented
  default, set via `dxlog_port` in `config.example.dxlog.json`.
- The status field in reports correctly says `"dxlog"`, not `"n1mm"`, so
  the origin is still visible on the status page even though the parser
  is shared.

**The one real caveat:** the "these two formats are identical" premise
rests on your recollection of requesting that compatibility mode years
ago, not a fresh capture of what DXLog actually sends today. That's
decent evidence, not nothing — but once you've got DXLog actually running
in this mode, a quick capture to confirm is worth doing, the same
discipline as everything else here. If it turns out to differ in some
field, that's a small patch to `n1mm.go`, not a rewrite, since it's
already structured to ignore anything it doesn't recognize rather than
crash on it.

Tested (in the same sandbox sense as everything else): a real UDP packet
sent to port 13063 (DXLog's documented port, not N1MM's), through the
actual compiled `dxlog`-mode binary, correctly reporting 14.0805 MHz,
USB, operator falling back to `mycall` when `OpCall` was empty — and the
log line correctly reads `[dxlog :13063]`, not `[n1mm :13063]`.

## Contest mode — the exact frequency never leaves the shack LAN

Set `"contest_mode": true` at the top level of the agent's config.json
(sibling of `"source"` — it's a whole-agent-process setting, not
per-radio, exactly like `"source"`). Default is `false`; omitting the key
entirely is the same as `false`.

**What it does:** the agent still reads the real operating frequency from
TCI / N1MM / DXLog internally, but it converts it to a US amateur *band
name* ("40M", "20M", …) and sends only that. The outbound report has
`band` set and `freq_hz` explicitly `null`. The server stores and serves
whatever it's given; the frontend shows the band string in the Freq
column when present, and the normal `14.0740 MHz` formatting otherwise.

**The privacy guarantee:** the exact VFO frequency is never transmitted
off the shack LAN and never reaches the server, the `/api/status` JSON,
or the browser. This is enforced agent-side, before the HTTPS push — not
as a UI filter over data that's still sitting in the public API response.
`mode` and `operator` are unaffected; contest mode only touches
frequency/band. All three sources (`tci`, `n1mm`, `dxlog`) inherit the
behavior automatically since they share the same report-building path.

**Band edges** are hardcoded in `agent/band.go` and were checked against
ARRL's published US allocations, not a remembered table:
- ARRL Frequency Allocations chart
  (https://www.arrl.org/frequency-allocations) and ARRL Band Plan
  (https://www.arrl.org/band-plan), cross-checked against each other, for
  160M through 23CM plus the 2200M / 630M LF/MF bands.
- 60M is not a contiguous allocation. Per the FCC rules effective
  2026-02-13 (ARRL: "New 60-Meter Frequencies Available as of February
  13", https://www.arrl.org/news/new-60-meter-frequencies-available-as-of-february-13),
  US amateurs have four 2.8 kHz channels centered on 5332 / 5348 / 5373 /
  5405 kHz plus a contiguous 5351.5–5366.5 kHz segment. `band.go`
  classifies the whole 5330.6–5406.4 kHz envelope (lowest channel edge to
  highest channel edge) as "60M" — finer resolution than the band name
  is exactly what contest mode is meant to withhold.
- A frequency in no recognized band returns the sentinel `"OOB"` (out of
  band), never an empty string or a panic.

`band.go`'s header comment carries the full table and reasoning;
`band_test.go` covers a real operating frequency in every band, inclusive
band-edge cases, and out-of-band inputs (including 0 and negative).
`contest_mode_test.go` proves the actual privacy property end to end
through the real snapshot code path: with `contest_mode` on a report's
`FreqHz` is `nil` and `Band` is populated; with it off (or unset),
behavior is byte-for-byte unchanged (`Band` nil, `FreqHz` sent as
normal).

## What's been tested (in a sandbox, not on your hardware)

- Go unit tests (`go test ./...` in `agent/`), including:
  - `TestRealAetherSDRCapture` — built from your actual captured TCI
    output (comma artifact included).
  - `TestN1MMOfficialExamplePacket` — built from N1MM's official example
    packet, confirming the tens-of-Hz math and OpCall parsing.
  - `TestN1MMOperatorFallsBackToMyCallThenDefault` — the OpCall → mycall
    → default_operator fallback chain.
  - `TestN1MMIgnoresNonRadioInfoPackets` — a ContactInfo-shaped packet on
    the same port doesn't get mistaken for radio state or crash anything.
  - `TestN1MMStaleAfterTimeout` — a radio with no recent packet reads as
    disconnected.
  - `config_test.go` — source validation for all three modes (missing
    tci_port/tci_host, missing dxlog_port, missing radio_nr, `--source`
    flag overriding a file with no `"source"` field, no source configured
    anywhere failing loudly).
  - Malformed-input cases for both sources that must not panic.
  - `band_test.go` — a real operating frequency in every band 2200M–23CM
    maps to the right name, inclusive band edges classify correctly, and
    out-of-band inputs (including 0 and a negative) return `"OOB"` without
    panicking. Band table is also asserted to be ascending and
    non-overlapping.
  - `contest_mode_test.go` — the privacy property, through the real
    snapshot code path for both TCI and N1MM/DXLog: with `contest_mode`
    on, a report's `FreqHz` is `nil` and `Band` is set; with it off or
    unset, `Band` is `nil` and `FreqHz` is sent unchanged. Also confirms
    `mode`/`operator` are untouched by contest mode and that the config
    field parses with no special validation (omitted ⇒ false).
- All binaries cross-compiled and confirmed by `file`: a real PE32+
  Windows binary, a static ELF64 x86-64 Linux binary, a static ELF64 ARM
  aarch64 binary (`stationagent-linux-arm64`, for ARM Chromebooks via
  Crostini), and a static ELF32 ARM EABI5 binary (`stationagent-linux-armv7`).
  The ARM builds are cross-compile + `file`-verified only — not yet run on
  actual ARM hardware.
- Full end-to-end run using the **actual compiled Linux binary**, twice:
  - TCI mode: a fake TCI WebSocket server → the agent binary → the real
    Python server → `/api/status` correctly Online with right freq/mode.
  - N1MM mode: a real UDP packet (the literal official example) sent over
    an actual socket → the agent binary's UDP listener → the real Python
    server → `/api/status` showing 3.52211 MHz, CW, operator W1ABC.
  - `--source` flag confirmed to override a config.json with no
    `"source"` field; missing source with no flag fails with a clear
    error instead of guessing a default.
  - DXLog mode: a real UDP packet sent to port 13063 (its documented
    port, distinct from N1MM's 12060) → the actual compiled `dxlog`-mode
    binary → the real server → `/api/status` showing 14.0805 MHz, USB,
    operator correctly falling back to `mycall`. Log line confirmed to
    read `[dxlog :13063]`, not `[n1mm :13063]`, despite sharing the parser.
  - Contest mode: a fake TCI source feeding 14.074 MHz → the **actual
    compiled Windows binary** with `"contest_mode": true` → the real
    Python server → `curl /api/status` showing `"band": "20M"` and
    `"freq_hz": null`, with the literal string `14074000` appearing
    nowhere in the response. The same binary with `"contest_mode": false`
    against the same source reported `"freq_hz": 14074000`, `"band":
    null` — unchanged from today. Confirmed the server's offline/staleness
    logic (which only looks at report timestamps and `connected`) doesn't
    choke on a null `freq_hz`.
- Server flow on its own: valid report → Online; bad auth token → 401;
  reports stop arriving → Offline past the threshold; multiple stations
  independently; `connected: false` → immediate Offline.
- Static frontend served correctly by the same process, and visually
  rendered in a headless browser against your real CSS/font/header.

Not tested: an actual AetherSDR or N1MM instance, running the agent on a
real Windows machine (only cross-compiled and confirmed as a valid
Windows executable), running the ARM builds on actual ARM hardware / a
Chromebook (cross-compiled and `file`-verified only), or the frontend in
a real (non-headless) browser.
