package main

// Last Station Worked -- an ADIF record per logged QSO, over UDP.
//
// This runs in the AGENT, on the shack computer, never on the VPS: the
// server has no ADIF endpoint and no UDP port, exactly like the N1MM path
// (see n1mm.go). The logger broadcasts on the shack LAN, the agent
// translates, and what leaves the LAN is the same authenticated HTTPS
// report the agent already sends.
//
// Unlike tci.go / n1mm.go this is NOT a source: it doesn't describe a
// radio's state and it never decides whether a station is online. It's an
// optional add-on that runs alongside whichever source the agent is in
// (`last_worked` / `adif_port` in config.json, see config.go) and
// contributes one station-level field to each report: date, time,
// callsign and mode of the last QSO the logger put on the air.
//
// Wire format, confirmed against QRZero's own source and user docs
// (https://github.com/nreed97/QRZero, Settings -> UDP connections, event
// "QSO logged", format "ADIF record" -- crates/server/src/udp_out.rs's
// Format::Adif, which calls crates/core/src/adif.rs's write_record):
//
//   <CALL:6>DL1ABC <QSO_DATE:8>20261011 <TIME_ON:6>011530 <BAND:3>20m <FREQ:7>14.0255 <MODE:2>CW <RST_SENT:3>599 <RST_RCVD:3>579 <EOR>
//
// One datagram, one record: `<FIELD:length>value ` pairs, upper-case field
// names, terminated by `<EOR>` and a newline. No ADIF header and no
// `<EOH>` -- this is a bare record, not a file, so a parser that insists
// on a header will reject it. The length in each tag is the authority on
// where a value ends: a COMMENT or NAME can contain spaces, so splitting
// on whitespace quietly truncates those fields.
//
// Only four of the record's fields are read, and only those four are
// reported: QSO_DATE, TIME_ON, CALL and MODE (CJ, 2026-10-11). Everything
// else a logger packs into an ADIF record -- frequency, reports, grid,
// name, QTH, the lot -- is ignored here and never leaves the shack LAN,
// which is also the privacy answer: there is nothing to mask because
// nothing else is sent. Adding a field later means adding it in three
// places (here, the report payload, the page), not loosening a filter.
//
// Things taken from the real format rather than assumed:
//   - MODE for a digital QSO is ADIF's parent mode with the real one in
//     SUBMODE (MFSK + FT4), so SUBMODE wins when it's there -- otherwise
//     an FT4 QSO would show up as "MFSK".
//   - QSO_DATE/TIME_ON are UTC, TIME_ON either HHMMSS or HHMM.
//
// Any logger that can send an ADIF record to a UDP port works; QRZero is
// just the one this was built and tested against. The default port is
// 14060, chosen to sit well clear of N1MM's 12060/12061/12062 (and
// DXLog's 13063) so pointing a logger at this can never collide with a
// radio-state broadcast -- including the one this same agent may be
// listening to in n1mm mode.

import (
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The read loop's timings mirror n1mm.go's, and for the same reasons (a
// UDP socket that stops receiving doesn't error out on its own) -- with
// one deliberate difference: there is no "stale" notion here. A QSO is an
// event, not a heartbeat. Hours of silence between contests is normal and
// must not make the last QSO disappear or mark anything offline.
const (
	adifReadPollInterval = 5 * time.Second
	adifReconnectBackoff = 2 * time.Second
	// An ADIF record with every field of a QSO is a few hundred bytes;
	// 64 KB is simply the largest a datagram can be, so nothing is ever
	// truncated (on Windows an oversized read fails outright rather than
	// truncating -- see n1mm.go).
	adifReadBufferSize = 65536
)

// adifSourceLabel tags every QSO that arrives this way, for the page's
// chip. "qrzero" because QRZero's ADIF-over-UDP connection is what this
// was built for and what a reader will recognise -- see lastWorked.Source
// on why it names the route rather than claiming to detect the sender.
const adifSourceLabel = "qrzero"

// adifDefaultPort is the port the agent listens on for ADIF records when
// the feature is enabled without naming one. Clear of N1MM (12060-12062)
// and DXLog (13063) -- see the file header.
const adifDefaultPort = 14060

// lastWorked is the station-level "last QSO logged" the agent reports:
// date, time, callsign, mode and nothing else. Mode is a pointer because
// a sparse ADIF record genuinely may not carry one, and "unknown" has to
// stay distinguishable from "empty string" all the way to the page.
type lastWorked struct {
	Call string  `json:"call"`
	Mode *string `json:"mode"`
	// Source names the integration the QSO arrived through, so the page
	// can chip it ("QRZero Logged"). It says which PORT it came in on,
	// not which program sent it -- nothing in an ADIF record identifies
	// the logger, and any logger that can send one to this port works.
	Source string `json:"source"`
	// At is the QSO's own time (UTC, RFC3339) from QSO_DATE + TIME_ON, or
	// the moment the record arrived if the record didn't say. One
	// timestamp rather than separate date and time fields: the page splits
	// it for display, and a single instant can't disagree with itself.
	At string `json:"at"`
}

type AdifSource struct {
	Port int

	mu   sync.Mutex
	last *lastWorked

	conn    *net.UDPConn
	stop    chan struct{}
	stopped bool
}

func NewAdifSource(port int) *AdifSource {
	return &AdifSource{Port: port, stop: make(chan struct{})}
}

// GetLastWorked returns the last QSO seen, or nil if none has arrived
// since the agent started. Deliberately never expires: the page shows the
// QSO's own date and time, so a reader can judge for themselves whether
// "last worked" means a minute ago or last weekend -- hiding it after a
// timeout would just replace a dated fact with no fact at all.
func (s *AdifSource) GetLastWorked() *lastWorked {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	copied := *s.last
	return &copied
}

// apply parses one datagram and, if it holds a usable QSO, makes it the
// new last-worked. A datagram with several records (QRZero sends one per
// QSO, but "Send through UDP connections" on a multi-row selection is
// right there in its menu) keeps the last one in the datagram.
func (s *AdifSource) apply(packet []byte, receivedAt time.Time) {
	var newest *lastWorked
	for _, rec := range parseAdifRecords(string(packet)) {
		if lw := recordToLastWorked(rec, receivedAt); lw != nil {
			newest = lw
		}
	}
	if newest == nil {
		return
	}
	s.mu.Lock()
	s.last = newest
	s.mu.Unlock()
	mode := "?"
	if newest.Mode != nil && *newest.Mode != "" {
		mode = *newest.Mode
	}
	log.Printf("[adif :%d] last worked: %s %s at %s", s.Port, newest.Call, mode, newest.At)
}

// recordToLastWorked turns one parsed ADIF record into a report, or nil if
// it has no callsign -- the one field without which there is nothing to
// show. Mode and the timestamp are optional by design: an ADIF record is
// whatever the logger chose to put in it, and a missing field is not a
// reason to drop a QSO on the floor.
func recordToLastWorked(rec map[string]string, receivedAt time.Time) *lastWorked {
	call := strings.ToUpper(strings.TrimSpace(rec["CALL"]))
	if call == "" {
		return nil
	}

	lw := &lastWorked{Call: call, Source: adifSourceLabel, At: receivedAt.UTC().Format(time.RFC3339)}

	// SUBMODE wins over MODE: ADIF stores FT4 as MODE=MFSK SUBMODE=FT4.
	mode := strings.ToUpper(strings.TrimSpace(rec["SUBMODE"]))
	if mode == "" {
		mode = strings.ToUpper(strings.TrimSpace(rec["MODE"]))
	}
	if mode != "" {
		lw.Mode = &mode
	}

	if at, ok := parseAdifTime(rec["QSO_DATE"], rec["TIME_ON"]); ok {
		lw.At = at.Format(time.RFC3339)
	}
	return lw
}

// parseAdifTime combines ADIF's QSO_DATE (YYYYMMDD) and TIME_ON (HHMMSS,
// or HHMM from a logger that omits seconds) into a UTC time. ADIF times
// are always UTC.
func parseAdifTime(date, timeOn string) (time.Time, bool) {
	date = strings.TrimSpace(date)
	timeOn = strings.TrimSpace(timeOn)
	if len(date) != 8 {
		return time.Time{}, false
	}
	layout, value := "20060102", date
	switch len(timeOn) {
	case 6:
		layout, value = "20060102150405", date+timeOn
	case 4:
		layout, value = "200601021504", date+timeOn
	case 0:
		// Date with no time: midnight UTC is the only honest reading.
	default:
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(layout, value, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// parseAdifRecords pulls every `<FIELD:len>value` pair out of an ADIF
// fragment, split into records on `<EOR>`. Field names are upper-cased;
// the declared length decides where a value ends, so values containing
// spaces (COMMENT, NAME, QTH) survive intact.
//
// Tolerant on purpose -- this reads whatever a logger on the LAN sends,
// and a malformed datagram must be ignored rather than crash the agent or
// poison the last-worked with half a record:
//   - a trailing record with no `<EOR>` is still returned (some senders
//     omit it; QRZero doesn't, but this costs nothing)
//   - an ADIF *file* with a header and `<EOH>` works too: the header's
//     fields are discarded at the `<EOH>` rather than leaking into the
//     first real record
//   - a tag with a non-numeric length, a length that runs past the end of
//     the data, or an unterminated `<` is skipped, and parsing continues
//     with the rest
//   - `<FIELD:len:T>` type indicators are accepted and the type ignored
func parseAdifRecords(text string) []map[string]string {
	var records []map[string]string
	current := map[string]string{}

	for i := 0; i < len(text); {
		open := strings.IndexByte(text[i:], '<')
		if open < 0 {
			break
		}
		i += open + 1
		end := strings.IndexByte(text[i:], '>')
		if end < 0 {
			break // unterminated tag: nothing usable left
		}
		tag := text[i : i+end]
		i += end + 1

		parts := strings.Split(tag, ":")
		name := strings.ToUpper(strings.TrimSpace(parts[0]))

		if name == "EOR" {
			if len(current) > 0 {
				records = append(records, current)
				current = map[string]string{}
			}
			continue
		}
		if name == "EOH" || len(parts) < 2 {
			// A header boundary, or a tag with no length (so no value to
			// read). Either way, start fresh -- a header's fields must not
			// leak into the first real record.
			current = map[string]string{}
			continue
		}

		length, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || length < 0 || i+length > len(text) {
			continue // bad or over-long length: skip this field only
		}
		if name != "" {
			current[name] = text[i : i+length]
		}
		i += length
	}

	if len(current) > 0 {
		records = append(records, current)
	}
	return records
}

// Run listens for ADIF datagrams, rebinding with backoff if the socket
// fails, until Stop() is called. Unlike n1mm.go's read loop there is no
// idle rebind: silence here is the normal state between QSOs, so there is
// no interval after which "nothing has arrived" is evidence of anything.
func (s *AdifSource) Run() {
	for {
		if s.isStopped() {
			return
		}

		conn, err := s.listen()
		if err != nil {
			log.Printf("[adif :%d] failed to listen: %v (retrying in %v)", s.Port, err, adifReconnectBackoff)
			if s.waitOrStop(adifReconnectBackoff) {
				return
			}
			continue
		}

		log.Printf("[adif :%d] listening for ADIF records (last station worked)", s.Port)
		s.readLoop(conn)
		conn.Close()

		if s.isStopped() {
			return
		}
		log.Printf("[adif :%d] rebinding after read error", s.Port)
		if s.waitOrStop(adifReconnectBackoff) {
			return
		}
	}
}

func (s *AdifSource) listen() (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: s.Port, IP: net.IPv4zero})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	return conn, nil
}

func (s *AdifSource) readLoop(conn *net.UDPConn) {
	buf := make([]byte, adifReadBufferSize)
	for {
		conn.SetReadDeadline(time.Now().Add(adifReadPollInterval))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if s.isStopped() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue // just polling; no QSO yet, which is normal
			}
			log.Printf("[adif :%d] read error: %v", s.Port, err)
			return
		}
		packet := make([]byte, n)
		copy(packet, buf[:n])
		s.apply(packet, time.Now())
	}
}

func (s *AdifSource) isStopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *AdifSource) waitOrStop(d time.Duration) bool {
	select {
	case <-time.After(d):
		return false
	case <-s.stop:
		return true
	}
}

func (s *AdifSource) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stop)
	if s.conn != nil {
		s.conn.Close()
	}
}
