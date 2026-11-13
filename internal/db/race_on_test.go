//go:build race

package db

// raceEnabled: the tests run with -race (about 10 times slower): timing checks are skipped.
const raceEnabled = true

// slowdown: the race detector makes the code 5-10 times slower; the timing limits of the tests are
// multiplied by this (what they check is the same: no rescans, no reading the pages, writes go through).
const slowdown = 10
