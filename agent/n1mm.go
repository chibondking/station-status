package main

// N1MM / DXLog data source.
//
// Also used for DXLog when DXLog is configured to broadcast radio
// information in its N1MM-compatible mode (Options|Broadcast|Radio
// information, with the "N1MM-like format" option enabled) -- per direct
// confirmation that this mode exists and was specifically requested
// (years ago, for a Node-RED dashboard) to reproduce N1MM's own
// broadcast schema. That's real evidence, not nothing, but it's still
// secondhand recollection rather than a fresh capture -- worth a quick
// verification once DXLog is actually running in this mode, the same
// way everything else here was checked against real data. If DXLog's
// actual output differs, the fix is a small patch here, not a rewrite.
//
// Confirmed against N1MM Logger+'s official documentation
// (https://n1mmwp.hamdocs.com/appendices/external-udp-broadcasts/),
// including a literal example RadioInfo packet:
//
//   <?xml version="1.0" encoding="utf-8"?>
//   <RadioInfo>
//   <app>N1MM</app>
//   <StationName>CW-80m</StationName>
//   <RadioNr>1</RadioNr>
//   <Freq>352211</Freq>
//   <TXFreq>352211</TXFreq>
//   <Mode>CW</Mode>
//   <mycall>W1ABC</mycall>
//   <OpCall>W1ABC</OpCall>
//   <IsRunning>False</IsRunning>
//   ... (additional fields not needed here)
//   </RadioInfo>
//
// Important, easy-to-miss detail confirmed by that example: Freq/TXFreq
// are in TENS OF HZ, not Hz -- 352211 only makes sense as 3.52211 MHz
// (matching the example's own "CW-80m" station name) once multiplied by
// 10. This is unlike TCI, which uses raw Hz. Getting this wrong would
// silently report every frequency as 1/10th of reality.
//
// RadioNr is 1-based (1 or 2), matching N1MM's own SO2R/SO2V numbering --
// deliberately NOT renumbered to 0-based like TCI's receivers, to avoid
// a second silent off-by-one on top of the panadapter-numbering one we
// already hit once.
//
// N1MM broadcasts several other packet types (ContactInfo, Spot, Score,
// Application Info, etc.) on the same port if the user enables them.
// Only <RadioInfo> is handled here; anything else fails to unmarshal into
// the RadioInfo struct (because the XML root element name won't match)
// and is silently ignored, which is the correct behavior, not a bug.
//
// N1MM sends RadioInfo at least every 10 seconds, or immediately on
// change. IsConnected() here reflects "received a RadioInfo packet for
// this radio number within the last few heartbeat intervals", not a
// persistent connection the way TCI has one -- UDP broadcast has no
// concept of a connection to begin with.

import (
	"encoding/xml"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// n1mmStaleAfter is how long without a RadioInfo packet before we call a
// radio disconnected. N1MM's own heartbeat is ~10s; this allows for a
// couple of missed beats plus network jitter before giving up.
const n1mmStaleAfter = 25 * time.Second

type n1mmRadioInfo struct {
	XMLName xml.Name `xml:"RadioInfo"`
	RadioNr int      `xml:"RadioNr"`
	Freq    int64    `xml:"Freq"`    // tens of Hz -- see file header
	Mode    string   `xml:"Mode"`
	MyCall  string   `xml:"mycall"`
	OpCall  string   `xml:"OpCall"`
}

type n1mmRadioState struct {
	FreqHz     int64
	Mode       string
	Operator   string
	LastUpdate time.Time
}

type N1MMSource struct {
	Port            int
	DefaultOperator string
	// Label is used only in log lines, so DXLog mode doesn't print
	// "[n1mm ...]" and confuse anyone debugging a DXLog-only setup --
	// this is genuinely the same parser/struct for both, just logged
	// under whichever name actually applies.
	Label string

	mu     sync.Mutex
	radios map[int]*n1mmRadioState
	conn   *net.UDPConn
}

func NewN1MMSource(port int, defaultOperator string, label string) *N1MMSource {
	return &N1MMSource{
		Port:            port,
		DefaultOperator: defaultOperator,
		Label:           label,
		radios:          make(map[int]*n1mmRadioState),
	}
}

// GetRadio returns the last known state for the given N1MM RadioNr (1 or
// 2), and whether it's still considered connected (a packet arrived
// recently enough).
func (s *N1MMSource) GetRadio(radioNr int) (freqHz *int64, mode *string, operator *string, connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.radios[radioNr]
	if !ok {
		return nil, nil, nil, false
	}
	connected = time.Since(st.LastUpdate) < n1mmStaleAfter
	freq := st.FreqHz
	mode = &st.Mode
	op := st.Operator
	return &freq, mode, &op, connected
}

func (s *N1MMSource) apply(packet []byte) {
	var info n1mmRadioInfo
	if err := xml.Unmarshal(packet, &info); err != nil {
		// Not a RadioInfo packet (could be ContactInfo, Spot, Score,
		// etc. on the same port) -- expected, not an error.
		return
	}
	if info.RadioNr == 0 {
		// Unmarshaled but doesn't look like a real RadioInfo payload.
		return
	}

	operator := strings.TrimSpace(info.OpCall)
	if operator == "" {
		operator = strings.TrimSpace(info.MyCall)
	}
	if operator == "" {
		operator = s.DefaultOperator
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.radios[info.RadioNr]
	if !ok {
		st = &n1mmRadioState{}
		s.radios[info.RadioNr] = st
	}
	st.FreqHz = info.Freq * 10 // tens of Hz -> Hz, confirmed in file header
	st.Mode = strings.ToUpper(strings.TrimSpace(info.Mode))
	st.Operator = operator
	st.LastUpdate = time.Now()
}

// Run listens for UDP broadcasts until Stop() is called (closing the
// socket, which unblocks ReadFromUDP with an error).
func (s *N1MMSource) Run() {
	addr := &net.UDPAddr{Port: s.Port, IP: net.IPv4zero}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Printf("[%s :%d] failed to listen: %v", s.Label, s.Port, err)
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	log.Printf("[%s :%d] listening for RadioInfo broadcasts", s.Label, s.Port)
	buf := make([]byte, 8192)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // socket closed via Stop(), or a real error -- either way, exit
		}
		// Copy before handing off -- buf is reused on the next read.
		packet := make([]byte, n)
		copy(packet, buf[:n])
		s.apply(packet)
	}
}

func (s *N1MMSource) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
	}
}
