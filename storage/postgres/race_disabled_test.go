//go:build !race

package postgres_test

// raceEnabled reports whether the test binary was built with the race
// detector. Throughput assertions are skipped under -race because the
// instrumentation changes wall-clock timings by an order of magnitude.
const raceEnabled = false
