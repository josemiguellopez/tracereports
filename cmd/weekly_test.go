package main

import (
	"testing"
	"time"
)

func TestWeeklySchedule(t *testing.T) {
	day, h, m, err := weeklySchedule(" Mon 09:30 ")
	if err != nil || day != time.Monday || h != 9 || m != 30 {
		t.Fatalf("parse: %v %d %d %v", day, h, m, err)
	}
	for _, bad := range []string{"monday 9", "lun 09:00", "mon", "mon 25:00", "mon 9am"} {
		if _, _, _, err := weeklySchedule(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestNextWeekly(t *testing.T) {
	loc := time.FixedZone("CL", -3*3600)
	wed := time.Date(2026, 10, 7, 10, 0, 0, 0, loc) // miércoles
	for _, c := range []struct {
		now  time.Time
		want time.Time
	}{
		{wed, time.Date(2026, 10, 12, 9, 0, 0, 0, loc)},                                       // el lunes siguiente
		{time.Date(2026, 10, 12, 8, 59, 0, 0, loc), time.Date(2026, 10, 12, 9, 0, 0, 0, loc)}, // el mismo lunes, antes de la hora
		{time.Date(2026, 10, 12, 9, 0, 0, 0, loc), time.Date(2026, 10, 19, 9, 0, 0, 0, loc)},  // justo a la hora: la próxima semana
	} {
		if got := nextWeekly(c.now, time.Monday, 9, 0); !got.Equal(c.want) {
			t.Errorf("nextWeekly(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}
