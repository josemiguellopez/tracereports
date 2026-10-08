package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

// weeklySchedule reads TRACEREPORTS_WEEKLY_SUMMARY: "mon 09:00" (weekday and time, server time).
func weeklySchedule(spec string) (time.Weekday, int, int, error) {
	f := strings.Fields(strings.ToLower(spec))
	if len(f) != 2 {
		return 0, 0, 0, fmt.Errorf("TRACEREPORTS_WEEKLY_SUMMARY must look like \"mon 09:00\", not %q", spec)
	}
	day, ok := weekdays[f[0]]
	if !ok {
		return 0, 0, 0, fmt.Errorf("TRACEREPORTS_WEEKLY_SUMMARY: day must be sun, mon, tue, wed, thu, fri or sat")
	}
	t, err := time.Parse("15:04", f[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("TRACEREPORTS_WEEKLY_SUMMARY: time must be HH:MM")
	}
	return day, t.Hour(), t.Minute(), nil
}

// nextWeekly is the next moment after now that falls on day at hour:minute.
func nextWeekly(now time.Time, day time.Weekday, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	for next.Weekday() != day || !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// weeklyClock is the scheduler's clock: the real one in production, a controlled one in tests.
type weeklyClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var schedClock weeklyClock = realClock{}

// startWeekly posts the weekly summary to Teams/Slack at the configured time.
func startWeekly(ctx context.Context, spec string, srv *api.Server, n *notify.Notifier) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	day, hour, minute, err := weeklySchedule(spec)
	if err != nil {
		return err
	}
	if !n.Enabled() {
		slog.Warn("TRACEREPORTS_WEEKLY_SUMMARY is set but no channel is (TEAMS_WEBHOOK_URL or SLACK_WEBHOOK_URL): nothing will be sent")
		return nil
	}
	clock := schedClock
	slog.Info("weekly summary enabled", "next", nextWeekly(clock.Now(), day, hour, minute).Format(time.RFC1123))
	go func() {
		// el turno se fija antes de esperar y es la clave del envío (outbox): un timer que despierta
		// tarde no puede tomar el turno de la semana siguiente
		slot := nextWeekly(clock.Now(), day, hour, minute)
		for {
			select {
			case <-ctx.Done():
				return
			case <-clock.After(slot.Sub(clock.Now())):
			}
			e, err := srv.WeeklySummary("")
			if err == nil {
				sctx, cancel := context.WithTimeout(ctx, time.Minute)
				_, err = n.SendWeeklyScheduled(sctx, e, slot)
				cancel()
			}
			if err != nil {
				slog.Error("weekly summary", "err", err)
			}
			// el próximo turno es posterior a este y a ahora. Si el proceso estuvo suspendido varias
			// semanas se envió un solo resumen (el del turno esperado, con los últimos 7 días de
			// ahora) y las semanas perdidas no se recuperan una tras otra
			now := clock.Now()
			if now.Before(slot) {
				now = slot
			}
			slot = nextWeekly(now, day, hour, minute)
		}
	}()
	return nil
}
