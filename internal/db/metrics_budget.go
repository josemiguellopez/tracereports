package db

import (
	"errors"
	"time"
)

// MaxMetricsDays is the budget of a metrics period: at most this many calendar dates (366, a
// whole year with February 29). The daily chart has one point per date, so a response stays
// small whatever range is asked; a longer one is rejected (ErrMetricsRangeTooLong), never cut.
const MaxMetricsDays = 366

// ErrMetricsRangeTooLong is a period longer than MaxMetricsDays.
var ErrMetricsRangeTooLong = errors.New("metrics period too long")

// dateCount is how many calendar dates (local) go from a to b, both included, computed without
// listing them.
func dateCount(a, b time.Time) int {
	day := func(t time.Time) time.Time {
		y, m, d := t.In(time.Local).Date()
		return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	}
	return int(day(b).Sub(day(a))/(24*time.Hour)) + 1
}
