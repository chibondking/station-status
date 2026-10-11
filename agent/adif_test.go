package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// qrzeroRecord is the shape QRZero actually puts on the wire for a logged
// QSO: a bare record (no header, no <EOH>), `<FIELD:len>value ` pairs with
// upper-case names, every field of the QSO, ending `<EOR>\n`. Taken from
// crates/core/src/adif.rs's write_record and its FIELD_ORDER, not invented
// here -- see adif.go's header.
const qrzeroRecord = "<CALL:6>DL1ABC <QSO_DATE:8>20261011 <TIME_ON:6>011530 <BAND:3>20m " +
	"<FREQ:7>14.0255 <MODE:2>CW <RST_SENT:3>599 <RST_RCVD:3>579 " +
	"<STATION_CALLSIGN:4>WT2P <OPERATOR:4>WT2P <NAME:9>Hans Dorf " +
	"<COMMENT:20>nice signal, tnx QSO <EOR>\n"

func TestRecordToLastWorkedFromQRZeroRecord(t *testing.T) {
	recs := parseAdifRecords(qrzeroRecord)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d: %#v", len(recs), recs)
	}
	lw := recordToLastWorked(recs[0], time.Now())
	if lw == nil {
		t.Fatal("expected a last-worked report")
	}
	if lw.Call != "DL1ABC" {
		t.Errorf("call = %q, want DL1ABC", lw.Call)
	}
	if lw.Mode == nil || *lw.Mode != "CW" {
		t.Errorf("mode = %v, want CW", lw.Mode)
	}
	if lw.At != "2026-10-11T01:15:30Z" {
		t.Errorf("at = %q, want 2026-10-11T01:15:30Z", lw.At)
	}
	// 14.0255 MHz masked to its band name, via band.go's own table --
	// upper case, like a contest-mode radio row, not ADIF's "20m".
	if lw.Band == nil || *lw.Band != "20M" {
		t.Errorf("band = %v, want 20M", lw.Band)
	}
}

// The mask itself: the frequency is read and thrown away, and the band
// comes from band.go rather than from ADIF's own spelling. This is the
// test that fails if someone starts reporting FREQ.
func TestLastWorkedMasksFrequencyToBand(t *testing.T) {
	// FREQ present: band is derived, whatever BAND says.
	rec := parseAdifRecords("<CALL:4>W1AW <FREQ:8>21.00500 <BAND:3>20m <EOR>")[0]
	lw := recordToLastWorked(rec, time.Now())
	if lw.Band == nil || *lw.Band != "15M" {
		t.Errorf("band = %v, want 15M derived from FREQ (not BAND's 20m)", lw.Band)
	}
	// No FREQ: fall back to ADIF's BAND, upper-cased.
	rec = parseAdifRecords("<CALL:4>W1AW <BAND:4>70cm <EOR>")[0]
	if lw := recordToLastWorked(rec, time.Now()); lw.Band == nil || *lw.Band != "70CM" {
		t.Errorf("band = %v, want 70CM from ADIF's BAND", lw.Band)
	}
	// Neither: no band rather than a guess.
	rec = parseAdifRecords("<CALL:4>W1AW <MODE:2>CW <EOR>")[0]
	if lw := recordToLastWorked(rec, time.Now()); lw.Band != nil {
		t.Errorf("band = %v, want nil with neither FREQ nor BAND", lw.Band)
	}
	// A frequency in no amateur band gets band.go's sentinel, not a lie.
	rec = parseAdifRecords("<CALL:4>W1AW <FREQ:6>27.555 <EOR>")[0]
	if lw := recordToLastWorked(rec, time.Now()); lw.Band == nil || *lw.Band != "OOB" {
		t.Errorf("band = %v, want OOB", lw.Band)
	}
	// Rounding: 14.0255 MHz must not land a hair under 14,025,500 Hz.
	if hz, ok := parseAdifFreqHz("14.0255"); !ok || hz != 14_025_500 {
		t.Errorf("parseAdifFreqHz(14.0255) = %d, %v; want 14025500, true", hz, ok)
	}
}

// A value with spaces in it is why the declared length has to be the
// authority on where a field ends -- splitting on whitespace would cut
// COMMENT short and, worse, leave "signal," looking like the next tag.
func TestParseAdifRecordsKeepsValuesWithSpaces(t *testing.T) {
	rec := parseAdifRecords(qrzeroRecord)[0]
	if got := rec["COMMENT"]; got != "nice signal, tnx QSO" {
		t.Errorf("COMMENT = %q, want the whole phrase", got)
	}
	if got := rec["NAME"]; got != "Hans Dorf" {
		t.Errorf("NAME = %q, want \"Hans Dorf\"", got)
	}
}

// Only the agreed fields are reported. This is the test that fails if
// somebody widens the payload without widening the decision -- the
// reports, the name and the comment are right there in the record, and
// the exact frequency is deliberately replaced by its band.
func TestLastWorkedReportsOnlyDateTimeCallModeBand(t *testing.T) {
	lw := recordToLastWorked(parseAdifRecords(qrzeroRecord)[0], time.Now())
	raw, err := json.Marshal(lw)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	// The band name is reported; the frequency it came from is not.
	for _, leaked := range []string{"14.0255", "freq", "FREQ", "14025500", "599", "579", "Hans", "COMMENT", "comment"} {
		if strings.Contains(body, leaked) {
			t.Errorf("report leaked %q: %s", leaked, body)
		}
	}
	for _, want := range []string{`"call":"DL1ABC"`, `"mode":"CW"`, `"band":"20M"`, `"at":"2026-10-11T01:15:30Z"`, `"source":"qrzero"`} {
		if !strings.Contains(body, want) {
			t.Errorf("report missing %s: %s", want, body)
		}
	}
}

func TestRecordToLastWorkedSubmodeWinsOverMode(t *testing.T) {
	// ADIF stores FT4 as MODE=MFSK with SUBMODE=FT4; showing "MFSK" would
	// be technically true and useless.
	rec := parseAdifRecords("<CALL:5>K9CT <MODE:4>MFSK <SUBMODE:3>FT4 <EOR>")[0]
	lw := recordToLastWorked(rec, time.Now())
	if lw.Mode == nil || *lw.Mode != "FT4" {
		t.Errorf("mode = %v, want FT4", lw.Mode)
	}
}

func TestRecordToLastWorkedRequiresCall(t *testing.T) {
	recs := parseAdifRecords("<QSO_DATE:8>20261011 <TIME_ON:6>011530 <MODE:2>CW <EOR>")
	if len(recs) != 1 {
		t.Fatalf("expected the record to parse, got %#v", recs)
	}
	if lw := recordToLastWorked(recs[0], time.Now()); lw != nil {
		t.Errorf("expected nil for a record with no CALL, got %+v", lw)
	}
}

func TestRecordToLastWorkedFallsBackToArrivalTime(t *testing.T) {
	arrived := time.Date(2026, 10, 11, 2, 3, 4, 0, time.UTC)
	rec := parseAdifRecords("<CALL:4>W1AW <MODE:2>CW <EOR>")[0]
	lw := recordToLastWorked(rec, arrived)
	if lw.At != "2026-10-11T02:03:04Z" {
		t.Errorf("at = %q, want the arrival time", lw.At)
	}
	// A date with a 4-digit TIME_ON (a logger that omits seconds).
	rec = parseAdifRecords("<CALL:4>W1AW <QSO_DATE:8>20261011 <TIME_ON:4>0115 <EOR>")[0]
	if lw := recordToLastWorked(rec, arrived); lw.At != "2026-10-11T01:15:00Z" {
		t.Errorf("at = %q, want 2026-10-11T01:15:00Z", lw.At)
	}
	// A garbage time doesn't poison the record -- it falls back.
	rec = parseAdifRecords("<CALL:4>W1AW <QSO_DATE:8>20261011 <TIME_ON:3>011 <EOR>")[0]
	if lw := recordToLastWorked(rec, arrived); lw.At != "2026-10-11T02:03:04Z" {
		t.Errorf("at = %q, want the arrival time after an unparseable TIME_ON", lw.At)
	}
}

// Several records in one datagram: QRZero's "Send through UDP connections"
// on a multi-row selection. The last one is the last worked.
func TestApplyKeepsLastRecordInDatagram(t *testing.T) {
	s := NewAdifSource(adifDefaultPort)
	packet := "<CALL:4>W1AW <QSO_DATE:8>20261011 <TIME_ON:6>010000 <MODE:2>CW <EOR>\n" +
		"<CALL:4>K9CT <QSO_DATE:8>20261011 <TIME_ON:6>011500 <MODE:3>SSB <EOR>\n"
	s.apply([]byte(packet), time.Now())
	lw := s.GetLastWorked()
	if lw == nil || lw.Call != "K9CT" || lw.At != "2026-10-11T01:15:00Z" {
		t.Errorf("got %+v, want the second record (K9CT)", lw)
	}
}

// Whatever else is on the LAN must not become a last-worked, and must not
// overwrite a good one.
func TestApplyIgnoresJunkAndKeepsPreviousQSO(t *testing.T) {
	s := NewAdifSource(adifDefaultPort)
	s.apply([]byte(qrzeroRecord), time.Now())

	for _, junk := range []string{
		"",
		"hello",
		"<RadioInfo><RadioNr>1</RadioNr><Freq>1407400</Freq></RadioInfo>", // an N1MM broadcast
		"<CALL:99>truncated",   // length past the end of the data
		"<CALL:abc>W1AW <EOR>", // non-numeric length
		"<CALL:4>W1AW",         // no <EOR>, but no closing '>' problem either
		"<CALL",                // unterminated tag
	} {
		before := s.GetLastWorked()
		s.apply([]byte(junk), time.Now())
		after := s.GetLastWorked()
		if junk == "<CALL:4>W1AW" {
			// This one IS a usable record (a trailing record with no
			// <EOR> is accepted on purpose) -- reset for the next case.
			if after == nil || after.Call != "W1AW" {
				t.Errorf("a trailing record with no <EOR> should still count, got %+v", after)
			}
			s.apply([]byte(qrzeroRecord), time.Now())
			continue
		}
		if after == nil || after.Call != before.Call || after.At != before.At {
			t.Errorf("junk %q changed last-worked from %+v to %+v", junk, before, after)
		}
	}
}

// An ADIF *file* (header + <EOH>) rather than a bare record: the header's
// own fields must not be mistaken for a QSO.
func TestParseAdifRecordsDiscardsHeader(t *testing.T) {
	file := "Exported by QRZero\n<ADIF_VER:5>3.1.4 <PROGRAMID:6>QRZero <EOH>\n" +
		"<CALL:4>W1AW <MODE:2>CW <EOR>\n"
	recs := parseAdifRecords(file)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record after the header, got %d: %#v", len(recs), recs)
	}
	if _, ok := recs[0]["PROGRAMID"]; ok {
		t.Errorf("header fields leaked into the record: %#v", recs[0])
	}
}

// Lower-case field names: ADIF says names are case-insensitive, and
// nothing stops another logger from writing them that way.
func TestParseAdifRecordsIsCaseInsensitive(t *testing.T) {
	rec := parseAdifRecords("<call:4>w1aw <mode:2>cw <eor>")[0]
	lw := recordToLastWorked(rec, time.Now())
	if lw == nil || lw.Call != "W1AW" || lw.Mode == nil || *lw.Mode != "CW" {
		t.Errorf("got %+v, want W1AW/CW upper-cased", lw)
	}
}

// End to end over a real socket, so listen()/readLoop() are proven wired
// up and not just apply(). Same shape as n1mm_test.go's equivalent,
// including the unconnected sender socket -- see the comment there for why
// a connected one latches an ECONNREFUSED from the startup race.
func TestAdifSourceOverRealUDPSocket(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0, IP: net.IPv4zero})
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	src := NewAdifSource(port)
	go src.Run()
	defer src.Stop()

	sender, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0, IP: net.IPv4zero})
	if err != nil {
		t.Fatalf("failed to open a sender socket: %v", err)
	}
	defer sender.Close()
	dest := &net.UDPAddr{Port: port, IP: net.IPv4(127, 0, 0, 1)}

	for i := 0; i < 50; i++ {
		sender.WriteToUDP([]byte(qrzeroRecord), dest) // ignore errors -- retried below
		if lw := src.GetLastWorked(); lw != nil {
			if lw.Call != "DL1ABC" {
				t.Errorf("call = %q, want DL1ABC", lw.Call)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Run() never picked up an ADIF record sent over a real socket")
}

func TestAdifStopIsIdempotent(t *testing.T) {
	src := NewAdifSource(0)
	src.Stop()
	src.Stop() // must not panic
}

func TestResolveLastWorkedPortRules(t *testing.T) {
	// Off by default.
	cfg := &Config{}
	if err := resolveLastWorked(cfg); err != nil || cfg.LastWorked || cfg.AdifPort != 0 {
		t.Errorf("default should stay off, got err=%v cfg=%+v", err, cfg)
	}
	// On with the default port.
	cfg = &Config{LastWorked: true}
	if err := resolveLastWorked(cfg); err != nil || cfg.AdifPort != adifDefaultPort {
		t.Errorf("want port %d, got err=%v port=%d", adifDefaultPort, err, cfg.AdifPort)
	}
	// A port alone switches it on.
	cfg = &Config{AdifPort: 14070}
	if err := resolveLastWorked(cfg); err != nil || !cfg.LastWorked {
		t.Errorf("a port alone should enable it, got err=%v cfg=%+v", err, cfg)
	}
	// The collisions this is here to catch.
	cfg = &Config{LastWorked: true, AdifPort: 12060, N1MMPort: 12060}
	if err := resolveLastWorked(cfg); err == nil {
		t.Error("expected an error when adif_port equals n1mm_port")
	}
	cfg = &Config{LastWorked: true, AdifPort: 13063, DXLogPort: 13063}
	if err := resolveLastWorked(cfg); err == nil {
		t.Error("expected an error when adif_port equals dxlog_port")
	}
	cfg = &Config{LastWorked: true, AdifPort: 70000}
	if err := resolveLastWorked(cfg); err == nil {
		t.Error("expected an error for an out-of-range port")
	}
}
