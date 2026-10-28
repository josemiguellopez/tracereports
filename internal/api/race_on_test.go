//go:build race

package api

// raceEnabled: the tests run with -race. sync.Pool drops objects at random under the race
// detector, so allocation measurements vary: their limits are looser there.
const raceEnabled = true
