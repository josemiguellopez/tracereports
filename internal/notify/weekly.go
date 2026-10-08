package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

var weeklyText = map[string]map[string]string{
	"es": {"title": "Resumen semanal de pruebas", "runs": "Ejecuciones", "rate": "Tasa de éxito", "failed": "Tests que fallaron",
		"flaky": "Tests inestables", "top": "Lo que más falla", "recovered": "Ya se arreglaron", "risks": "Inestables a vigilar",
		"link": "Ver métricas", "headline_up": "La tasa de éxito subió {d} puntos: {r}%.", "headline_down": "La tasa de éxito bajó {d} puntos: {r}%.",
		"headline_same": "La tasa de éxito se mantuvo en {r}%.", "headline_new": "Tasa de éxito de la semana: {r}%.",
		"none": "Sin ejecuciones en los últimos {n} días.", "period": "Últimos {n} días", "fails": "falló {f} de {t} veces", "vs": "{v} (semana anterior: {p})"},
	"en": {"title": "Weekly test summary", "runs": "Runs", "rate": "Pass rate", "failed": "Failed tests",
		"flaky": "Flaky tests", "top": "Failing the most", "recovered": "Already fixed", "risks": "Flaky tests to watch",
		"link": "Open metrics", "headline_up": "The pass rate went up {d} points: {r}%.", "headline_down": "The pass rate went down {d} points: {r}%.",
		"headline_same": "The pass rate stayed at {r}%.", "headline_new": "Pass rate of the week: {r}%.",
		"none": "No runs in the last {n} days.", "period": "Last {n} days", "fails": "failed {f} of {t} times", "vs": "{v} (previous week: {p})"},
}

// Weekly builds the weekly summary from the metrics of the period: trend of the pass rate, what
// fails the most, what got fixed and the flaky tests to watch. Channel-agnostic, like an escalation.
func Weekly(m *db.Metrics, lang, publicURL string) *Escalation {
	l := weeklyText[lang]
	if l == nil {
		l = weeklyText["es"]
	}
	r := func(s string, kv ...string) string { return strings.NewReplacer(kv...).Replace(s) }
	pct := func(f float64) string {
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", f), "0"), ".")
	}
	cur, prev := m.Current, m.Previous
	e := &Escalation{Title: l["title"], LinkLabel: l["link"], Severity: r(l["period"], "{n}", fmt.Sprint(m.Days))}
	if publicURL != "" {
		e.URL = strings.TrimRight(publicURL, "/") + "/#view=metrics"
	}
	if cur.Runs == 0 {
		e.Headline = r(l["none"], "{n}", fmt.Sprint(m.Days))
		return e
	}
	diff := cur.PassRate - prev.PassRate
	switch {
	case prev.Runs == 0:
		e.Headline = r(l["headline_new"], "{r}", pct(cur.PassRate))
	case diff >= 0.5:
		e.Headline = r(l["headline_up"], "{d}", pct(diff), "{r}", pct(cur.PassRate))
	case diff <= -0.5:
		e.Headline = r(l["headline_down"], "{d}", pct(-diff), "{r}", pct(cur.PassRate))
		e.Critical = true
	default:
		e.Headline = r(l["headline_same"], "{r}", pct(cur.PassRate))
	}
	vs := func(now, before string) string {
		if prev.Runs == 0 {
			return now
		}
		return r(l["vs"], "{v}", now, "{p}", before)
	}
	e.Sections = [][2]string{
		{l["runs"], vs(fmt.Sprint(cur.Runs), fmt.Sprint(prev.Runs))},
		{l["rate"], vs(pct(cur.PassRate)+"%", pct(prev.PassRate)+"%")},
		{l["failed"], vs(fmt.Sprint(cur.Failed), fmt.Sprint(prev.Failed))},
		{l["flaky"], vs(fmt.Sprint(cur.FlakyTests), fmt.Sprint(prev.FlakyTests))},
	}
	var top, recovered []string
	for _, t := range m.TopFailing {
		line := fmt.Sprintf("%s — %s", t.Name, r(l["fails"], "{f}", fmt.Sprint(t.Fails), "{t}", fmt.Sprint(t.Runs)))
		if n := len(t.Recent); n > 0 && t.Recent[n-1] == "PASS" {
			recovered = append(recovered, t.Name)
			continue
		}
		if len(top) < 5 {
			top = append(top, line)
		}
	}
	var flaky []string
	for i, t := range m.Flaky {
		if i == 5 {
			break
		}
		flaky = append(flaky, t.Name)
	}
	for _, list := range [][2]any{{l["top"], top}, {l["recovered"], cap5(recovered)}, {l["risks"], flaky}} {
		if items := list[1].([]string); len(items) > 0 {
			e.Lists = append(e.Lists, list)
		}
	}
	return e
}

func cap5(s []string) []string {
	if len(s) > 5 {
		return s[:5]
	}
	return s
}

// SendWeekly posts the weekly summary to every configured channel ("send now"). Returns the
// channels it reached; an error if none is configured or every send failed. A channel that
// failed for a transient reason keeps it queued and retries it.
func (n *Notifier) SendWeekly(ctx context.Context, e *Escalation) ([]string, error) {
	return n.sendWeekly(ctx, e, "")
}

// SendWeeklyScheduled is the automatic weekly send of the slot (its day and time): the summary of
// a slot is sent to each channel once, even if the scheduler fires again for it.
func (n *Notifier) SendWeeklyScheduled(ctx context.Context, e *Escalation, slot time.Time) ([]string, error) {
	return n.sendWeekly(ctx, e, "weekly:"+slot.Format("2006-01-02T15:04"))
}

func (n *Notifier) sendWeekly(ctx context.Context, e *Escalation, slotKey string) ([]string, error) {
	teams, slack := n.Channels()
	if !teams && !slack {
		return nil, errors.New("no hay canal configurado (TEAMS_WEBHOOK_URL o SLACK_WEBHOOK_URL)")
	}
	var sent []string
	var errs []error
	for _, ch := range []struct {
		name string
		on   bool
	}{{"teams", teams}, {"slack", slack}} {
		if !ch.on {
			continue
		}
		var err error
		if slotKey == "" {
			err = n.sendOnDemand(ctx, "weekly", ch.name, e)
		} else {
			payload := map[string]any{"teams": TeamsEscalation(e), "slack": SlackEscalation(e)}[ch.name]
			err = n.deliver(ctx, "weekly", slotKey, ch.name, payload, false)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ch.name, err))
			continue
		}
		sent = append(sent, ch.name)
	}
	if len(sent) == 0 {
		return nil, errors.Join(errs...)
	}
	return sent, nil
}
