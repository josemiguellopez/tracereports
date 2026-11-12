//go:build !race

package db

const raceEnabled = false

// slowdown: see race_on_test.go.
const slowdown = 1
