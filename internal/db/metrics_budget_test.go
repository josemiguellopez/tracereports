package db

import (
	"errors"
	"testing"
	"time"
)

// El presupuesto también vale para quien llame a Metrics directamente: un rango o período más
// largo se rechaza antes de crear los días.
func TestMetricsRejectsPeriodsOverTheBudget(t *testing.T) {
	s := openTestStore(t)
	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)
	for _, q := range []MetricsQuery{
		{From: from.UnixMilli(), To: from.AddDate(100, 0, 0).UnixMilli()},
		{From: from.UnixMilli(), To: from.AddDate(0, 0, MaxMetricsDays+1).UnixMilli()},
		{Days: MaxMetricsDays + 1},
	} {
		if m, err := s.Metrics(q); !errors.Is(err, ErrMetricsRangeTooLong) || m != nil {
			t.Fatalf("%+v: %v", q, err)
		}
	}
	for _, q := range []MetricsQuery{
		{From: from.UnixMilli(), To: from.AddDate(0, 0, MaxMetricsDays).UnixMilli()},
		{Days: 365},
	} {
		m, err := s.Metrics(q)
		if err != nil || len(m.Daily) > MaxMetricsDays+1 {
			t.Fatalf("%+v: %v %d", q, err, len(m.Daily))
		}
	}
}
