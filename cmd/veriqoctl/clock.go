package main

import "time"

// zeroInstant is a fixed instant used by the reporting path.
//
// veriqoctl reports; it does not execute a case. Using a fixed instant
// rather than time.Now() means every report is byte-identical between
// runs, which is what lets a reader diff two of them and see only what
// actually changed.
func zeroInstant() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
