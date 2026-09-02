package main

// Frequency -> US amateur band name mapping.
//
// This exists for "contest mode" (see config.go's ContestMode): when
// enabled, the agent must send the *band name* ("20M") to the server
// instead of the exact operating frequency, so the precise VFO frequency
// never leaves the shack LAN. The real frequency is still read internally
// to feed freqToBand(); only the outbound report drops it.
//
// Band edges confirmed against ARRL's published US allocations, not a
// remembered table:
//
//   - ARRL Frequency Allocations chart
//     (https://www.arrl.org/frequency-allocations) and ARRL Band Plan
//     (https://www.arrl.org/band-plan), both cross-checked, agree on:
//       160M  1.800  - 2.000  MHz
//       80M   3.500  - 4.000  MHz
//       40M   7.000  - 7.300  MHz
//       30M   10.100 - 10.150 MHz
//       20M   14.000 - 14.350 MHz
//       17M   18.068 - 18.168 MHz
//       15M   21.000 - 21.450 MHz
//       12M   24.890 - 24.990 MHz
//       10M   28.000 - 29.700 MHz
//       6M    50.0   - 54.0   MHz
//       2M    144.0  - 148.0  MHz
//       1.25M 222.0  - 225.0  MHz
//       70CM  420.0  - 450.0  MHz
//       33CM  902.0  - 928.0  MHz
//       23CM  1240   - 1300   MHz
//     Plus the two LF/MF bands from the same chart:
//       2200M 0.1357 - 0.1378 MHz
//       630M  0.472  - 0.479  MHz
//
//   - 60M is the awkward one: it is not a contiguous allocation. As of the
//     FCC rules effective 2026-02-13 (ARRL: "New 60-Meter Frequencies
//     Available as of February 13",
//     https://www.arrl.org/news/new-60-meter-frequencies-available-as-of-february-13),
//     US amateurs have four 2.8 kHz channels centered on 5332, 5348, 5373
//     and 5405 kHz, plus a contiguous 5351.5-5366.5 kHz segment. Taking
//     each channel as center +/- 1.4 kHz, the lowest edge is 5330.6 kHz
//     (5332 - 1.4) and the highest is 5406.4 kHz (5405 + 1.4). We classify
//     anything in that 5330.6-5406.4 kHz envelope as "60M" -- a frequency
//     in one of the inter-channel gaps is still unambiguously "the 60m
//     band" for a display/privacy label, and contest mode never needs
//     finer resolution than the band name anyway.
//
// A frequency that falls in none of these ranges returns "OOB" (out of
// band) rather than "" or a panic -- an explicit, greppable sentinel the
// frontend/server can render as-is.

// band is one contiguous [lowHz, highHz] range (inclusive on both edges)
// and its display name. Ordered low to high; ranges do not overlap.
type band struct {
	name   string
	lowHz  int64
	highHz int64
}

// usBands covers the US amateur allocations relevant to HF/VHF/UHF
// operating. See the file header for the source of every edge.
var usBands = []band{
	{"2200M", 135_700, 137_800},
	{"630M", 472_000, 479_000},
	{"160M", 1_800_000, 2_000_000},
	{"80M", 3_500_000, 4_000_000},
	{"60M", 5_330_600, 5_406_400},
	{"40M", 7_000_000, 7_300_000},
	{"30M", 10_100_000, 10_150_000},
	{"20M", 14_000_000, 14_350_000},
	{"17M", 18_068_000, 18_168_000},
	{"15M", 21_000_000, 21_450_000},
	{"12M", 24_890_000, 24_990_000},
	{"10M", 28_000_000, 29_700_000},
	{"6M", 50_000_000, 54_000_000},
	{"2M", 144_000_000, 148_000_000},
	{"1.25M", 222_000_000, 225_000_000},
	{"70CM", 420_000_000, 450_000_000},
	{"33CM", 902_000_000, 928_000_000},
	{"23CM", 1_240_000_000, 1_300_000_000},
}

// oobBand is the sentinel returned for a frequency that isn't inside any
// recognized US amateur band. Deliberately not "" so it survives JSON
// round-trips and is obvious in a UI or a log.
const oobBand = "OOB"

// freqToBand maps an operating frequency in Hz to a US amateur band name
// ("20M", "40M", ...), or oobBand ("OOB") if it falls outside every
// recognized allocation. Band edges are inclusive.
func freqToBand(hz int64) string {
	for _, b := range usBands {
		if hz >= b.lowHz && hz <= b.highHz {
			return b.name
		}
	}
	return oobBand
}
