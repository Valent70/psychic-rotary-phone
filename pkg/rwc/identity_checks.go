package rwc

import "strconv"

// ValidateIMOCheckDigit applies the real IMO number check-digit
// algorithm (each of the first 6 digits multiplied by descending weights
// 7,6,5,4,3,2, summed, mod 10 must equal the 7th digit) — IMO Resolution
// A.600(15). This is a deterministic, offline, structural validation:
// real cryptographic-style arithmetic on the claimed identifier itself,
// not a network lookup. It is used as an INDEPENDENT (algorithmic, not
// broker-sourced) second opinion on a claimed vessel identity for
// RWC-002 — see run002.go — precisely because this environment has no
// live AIS/registry network access (baseline doc §1) and the brief
// forbids inventing corroboration that was not actually performed.
func ValidateIMOCheckDigit(imo string) (valid bool, reason string) {
	if len(imo) != 7 {
		return false, "IMO number must be 7 digits"
	}
	digits := make([]int, 7)
	for i, c := range imo {
		if c < '0' || c > '9' {
			return false, "IMO number must be all-numeric"
		}
		digits[i] = int(c - '0')
	}
	sum := 0
	for i, w := 0, 7; i < 6; i, w = i+1, w-1 {
		sum += digits[i] * w
	}
	check := sum % 10
	if check != digits[6] {
		return false, "check digit mismatch: computed " + strconv.Itoa(check) + " vs claimed " + strconv.Itoa(digits[6])
	}
	return true, "check digit verified"
}

// mmsiMID is a small, static table of Maritime Identification Digits
// (the first 3 digits of an MMSI, ITU-assigned per country) sufficient
// to structurally cross-check the flag/operator jurisdiction a vessel
// identity claim implies. Not exhaustive — only the entries this
// corpus's claims need are included, and an MID not in this table is
// honestly reported as "not in local reference table", never silently
// treated as valid or invalid.
var mmsiMID = map[string]string{
	"525": "Indonesia",
	"636": "Liberia",
	"477": "Hong Kong (China)",
	"353": "Panama",
	"366": "United States",
	"235": "United Kingdom",
	"257": "Norway",
}

// LookupMMSICountry returns the MID-implied flag state for an MMSI, and
// whether the MID was found in the local reference table at all.
func LookupMMSICountry(mmsi string) (country string, known bool) {
	if len(mmsi) < 3 {
		return "", false
	}
	c, ok := mmsiMID[mmsi[:3]]
	return c, ok
}
