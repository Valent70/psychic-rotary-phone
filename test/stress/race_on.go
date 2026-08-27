//go:build race

package stress

// raceEnabled reports whether the binary was built with the race
// detector. Throughput SLOs are stated for an uninstrumented build;
// asserting them under -race would either fail honestly-passing code or
// force the SLO down to a number that proves nothing about production.
const raceEnabled = true
