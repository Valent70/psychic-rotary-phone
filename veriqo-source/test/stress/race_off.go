//go:build !race

package stress

// raceEnabled reports whether the binary was built with the race
// detector. See race_on.go for why throughput SLOs are not asserted
// under instrumentation.
const raceEnabled = false
