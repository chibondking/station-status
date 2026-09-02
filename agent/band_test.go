package main

import "testing"

func TestFreqToBand(t *testing.T) {
	cases := []struct {
		name string
		hz   int64
		want string
	}{
		// One known real operating frequency per HF band (popular FT8 /
		// CW watering holes, or a typical phone frequency).
		{"160m FT8", 1_840_000, "160M"},
		{"80m digital", 3_573_000, "80M"},
		{"60m channel 3 center (5373 kHz)", 5_373_000, "60M"},
		{"40m FT8", 7_074_000, "40M"},
		{"30m FT8", 10_136_000, "30M"},
		{"20m FT8", 14_074_000, "20M"},
		{"17m FT8", 18_100_000, "17M"},
		{"15m FT8", 21_074_000, "15M"},
		{"12m FT8", 24_915_000, "12M"},
		{"10m FT8", 28_074_000, "10M"},
		{"6m FT8", 50_313_000, "6M"},
		{"2m SSB calling", 144_200_000, "2M"},
		{"1.25m FM simplex", 223_500_000, "1.25M"},
		{"70cm FM simplex", 446_000_000, "70CM"},
		{"33cm", 927_000_000, "33CM"},
		{"23cm", 1_296_100_000, "23CM"},
		{"2200m", 137_000, "2200M"},
		{"630m", 475_000, "630M"},

		// Band edges are inclusive -- the exact edge frequency must
		// classify as that band, not fall through to OOB.
		{"20m lower edge exactly", 14_000_000, "20M"},
		{"20m upper edge exactly", 14_350_000, "20M"},
		{"40m lower edge exactly", 7_000_000, "40M"},

		// Clearly out of any US amateur allocation -> OOB, no panic.
		{"AM broadcast", 1_000_000, "OOB"},
		{"between 20m and 17m", 15_000_000, "OOB"},
		{"just above 20m upper edge", 14_350_001, "OOB"},
		{"just below 40m lower edge", 6_999_999, "OOB"},
		{"CB / 11m", 27_185_000, "OOB"},
		{"FM broadcast", 100_100_000, "OOB"},
		{"zero", 0, "OOB"},
		{"negative (garbage)", -1, "OOB"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := freqToBand(tc.hz)
			if got != tc.want {
				t.Fatalf("freqToBand(%d) = %q, want %q", tc.hz, got, tc.want)
			}
		})
	}
}

// TestFreqToBandNoOverlap is a guard on the table itself: ranges must be
// ascending and non-overlapping, so a frequency can never match two bands
// (and the first-match loop is therefore unambiguous).
func TestFreqToBandNoOverlap(t *testing.T) {
	for i := 1; i < len(usBands); i++ {
		prev, cur := usBands[i-1], usBands[i]
		if cur.lowHz <= prev.highHz {
			t.Fatalf("band %s [%d..%d] overlaps or is out of order with %s [%d..%d]",
				cur.name, cur.lowHz, cur.highHz, prev.name, prev.lowHz, prev.highHz)
		}
		if cur.lowHz > cur.highHz {
			t.Fatalf("band %s has low %d > high %d", cur.name, cur.lowHz, cur.highHz)
		}
	}
}
