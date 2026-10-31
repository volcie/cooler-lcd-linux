//go:build linux

package main

import "runtime/debug"

// Tuning for a daemon that runs for weeks and does almost no work: one small
// frame every 200 ms. The defaults assume a short-lived, allocation-hungry
// process and hold far more headroom than this needs.
func init() {
	// Collect eagerly. The steady-state heap is a few hundred KiB, so trading a
	// little CPU for a smaller footprint is the right call; measured cost is
	// well under 1% of one core.
	debug.SetGCPercent(30)

	// Hard ceiling as a safety net if a future change starts allocating.
	debug.SetMemoryLimit(10 << 20)
}
