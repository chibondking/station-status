package main

// TCI data source.
//
// Confirmed against a real captured AetherSDR TCI session (not just docs):
//
//   vfo:<receiver>,<vfo_index>,<frequency_hz>;   -- the operating frequency.
//   modulation:<receiver>,<mode>;                -- mode, sent lowercase
//                                                    ("usb", "cw", etc.) on
//                                                    connect and on change;
//                                                    normalized to uppercase
//                                                    here for display.
//   trx:<receiver>,<true|false>[,<src>];         -- transmit on/off for that
//                                                    receiver/slice. The
//                                                    optional third field is
//                                                    what keyed it ("mic",
//                                                    "vox", "tci"); ignored
//                                                    here -- we only care
//                                                    that it IS transmitting,
//                                                    not what keyed it.
//
//   Also present in real traffic but intentionally ignored (not needed for
//   this use case, and safe to ignore -- unknown commands are no-ops):
//     dds:<receiver>,<frequency_hz>;      -- hardware/LO tuning point; can
//                                            stay fixed while vfo: moves
//                                            within the passband (e.g. fine
//                                            tuning without hardware retune)
//                                            -- confirmed via real capture
//                                            that vfo: and dds: diverge, and
//                                            vfo: is the one that tracks the
//                                            actual operating frequency.
//     rx_smeter:, rx_filter_band:, active_slice:, sql_enable:, etc.
//
//   Real capture also showed a stray leading comma joining two messages
//   (e.g. "rx_smeter:0,-91;,modulation:0,cw;") -- parseTCIMessage strips
//   leading commas from the command name to handle this.
//
// Still not independently confirmed: whether "receiver" 0/1 lines up with
// "Panadapter 1/2" the way you expect in your SO2R config (check this in
// AetherSDR's UI while tuning each), and the actual TCI port on your
// machine (check the TCI panel -- don't trust config.example.json's 40001).
//
// Also unconfirmed against a real capture: that AetherSDR names the
// transmit command "trx" specifically. "trx:" is the TCI spec's name for
// it and the one every other TCI implementation uses, but unlike vfo: and
// modulation: it has NOT been seen in a capture off your machine. The
// failure mode if it's named something else is benign and silent -- an
// unknown command is a no-op, so the [TX] chip simply never lights and
// nothing else about the page changes. To check, watch the stream while
// keying a slice (see README, "Is it transmitting?").

import (
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ReceiverState struct {
	FreqHz *int64
	Mode   *string
	// Tx is this receiver's live transmit state, straight off TCI's trx:
	// command. A plain bool, not a *bool: "we have never been told" and
	// "not transmitting" both mean the same thing to a reader of the page,
	// and conflating them is what keeps a [TX] chip from appearing on a
	// radio we simply haven't heard a trx: for yet.
	Tx         bool
	LastUpdate time.Time
}

// parseTCIMessage splits one WebSocket text frame into (command, args) pairs.
// A single frame can contain multiple ';'-terminated commands. Pure function,
// easy to unit test without a live connection.
func parseTCIMessage(raw string) [][2]interface{} {
	var results [][2]interface{}
	for _, chunk := range strings.Split(raw, ";") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" || !strings.Contains(chunk, ":") {
			continue
		}
		parts := strings.SplitN(chunk, ":", 2)
		// Trim stray leading commas too -- observed in real AetherSDR
		// capture as "rx_smeter:0,-91;,modulation:0,cw;", which splits
		// into a second chunk of ",modulation:0,cw" if not handled.
		cmd := strings.ToLower(strings.TrimSpace(strings.Trim(parts[0], ",")))
		var args []string
		if len(parts) > 1 && parts[1] != "" {
			args = strings.Split(parts[1], ",")
		}
		results = append(results, [2]interface{}{cmd, args})
	}
	return results
}

type TCISource struct {
	Host string
	Port int

	mu        sync.Mutex
	receivers map[int]*ReceiverState
	connected bool
	stop      chan struct{}
}

func NewTCISource(host string, port int) *TCISource {
	return &TCISource{
		Host:      host,
		Port:      port,
		receivers: make(map[int]*ReceiverState),
		stop:      make(chan struct{}),
	}
}

func (s *TCISource) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *TCISource) GetReceiver(index int) (freqHz *int64, mode *string, tx bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rx, ok := s.receivers[index]
	if !ok {
		return nil, nil, false
	}
	return rx.FreqHz, rx.Mode, rx.Tx
}

func (s *TCISource) getOrCreateReceiver(index int) *ReceiverState {
	rx, ok := s.receivers[index]
	if !ok {
		rx = &ReceiverState{}
		s.receivers[index] = rx
	}
	return rx
}

func (s *TCISource) apply(cmd string, args []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch cmd {
	case "vfo":
		if len(args) < 3 {
			return
		}
		receiver, err1 := strconv.Atoi(strings.TrimSpace(args[0]))
		freq, err2 := strconv.ParseInt(strings.TrimSpace(args[2]), 10, 64)
		if err1 != nil || err2 != nil {
			return
		}
		rx := s.getOrCreateReceiver(receiver)
		rx.FreqHz = &freq
		rx.LastUpdate = time.Now()
	case "modulation":
		if len(args) < 2 {
			return
		}
		receiver, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil {
			return
		}
		// Confirmed against real AetherSDR capture: mode comes through
		// lowercase ("usb", "cw"), not uppercase like some other TCI
		// implementations. Normalize to uppercase for consistent display.
		mode := strings.ToUpper(strings.TrimSpace(args[1]))
		rx := s.getOrCreateReceiver(receiver)
		rx.Mode = &mode
		rx.LastUpdate = time.Now()
	case "trx":
		if len(args) < 2 {
			return
		}
		receiver, err1 := strconv.Atoi(strings.TrimSpace(args[0]))
		// ParseBool rather than == "true": it accepts the "1"/"0" and
		// "True"/"False" spellings too, and more importantly it tells us
		// when the field is something we don't understand at all. On an
		// unparseable value we leave the previous state alone instead of
		// guessing -- treating garbage as "false" would drop a [TX] chip
		// mid-transmission, which is the one direction that misleads.
		txOn, err2 := strconv.ParseBool(strings.TrimSpace(args[1]))
		if err1 != nil || err2 != nil {
			return
		}
		// A third arg (what keyed it: "mic", "vox", "tci") may follow; it
		// doesn't change whether the radio is transmitting, so ignore it.
		rx := s.getOrCreateReceiver(receiver)
		rx.Tx = txOn
		rx.LastUpdate = time.Now()
	}
}

// Run connects with reconnect-with-backoff and blocks until Stop() is called.
func (s *TCISource) Run() {
	u := url.URL{Scheme: "ws", Host: s.Host + ":" + strconv.Itoa(s.Port)}
	backoff := 1 * time.Second

	for {
		select {
		case <-s.stop:
			return
		default:
		}

		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			log.Printf("[tci %s] connect failed: %v (retrying in %v)", u.String(), err, backoff)
			s.setConnected(false)
			select {
			case <-time.After(backoff):
			case <-s.stop:
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		log.Printf("[tci %s] connected", u.String())
		s.setConnected(true)
		backoff = 1 * time.Second

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("[tci %s] read error: %v", u.String(), err)
				s.setConnected(false)
				conn.Close()
				break
			}
			for _, pair := range parseTCIMessage(string(message)) {
				cmd := pair[0].(string)
				args := pair[1].([]string)
				s.apply(cmd, args)
			}
		}

		select {
		case <-s.stop:
			return
		default:
		}
	}
}

func (s *TCISource) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	// Dropping the connection clears every receiver's transmit state. TCI
	// only tells us TX ended by sending trx:<rx>,false, so a socket that
	// dies mid-transmission would otherwise leave the last trx:true we saw
	// as the newest thing we know -- and the page would show a radio stuck
	// in [TX] forever. Frequency and mode are left as they are: a stale
	// frequency is still the last place that radio was, and the server
	// blanks it anyway once the radio reads offline.
	if !v {
		for _, rx := range s.receivers {
			rx.Tx = false
		}
	}
	s.mu.Unlock()
}

func (s *TCISource) Stop() {
	close(s.stop)
}
