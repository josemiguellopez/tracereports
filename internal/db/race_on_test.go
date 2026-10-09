//go:build race

package db

// raceEnabled: the tests run with -race (about 10 times slower): timing checks are skipped.
const raceEnabled = true
