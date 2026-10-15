package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Summary is what Seed loaded, for the guide.
type Summary struct {
	Runs     int
	Nights   int // noches de historia
	Tests    int
	Failed   int
	Showcase int64 // la ejecución con más que ver: la suite de pytest + Playwright (se abre sola)
	APIRun   int64 // la suite de API por REST
}

// historyNights is how many earlier nights the demo has (Metrics, flaky, history).
const historyNights = 12

// Seed loads the demo into the TraceReports server at base: only scenarios recorded with the real
// clients, played at their dates. Twelve earlier nights (green, with a backend failure and a
// flaky test on some of them) and tonight, when the orders backend broke: the whole suite of
// each client plus the REST and JUnit XML runs. The captured calls point to the shop at shopURL,
// so the Lab can resend them.
func Seed(ctx context.Context, base, shopURL string, now time.Time) (*Summary, error) {
	base = strings.TrimRight(base, "/")
	sum := &Summary{Nights: historyNights}
	play := func(id string, at time.Time) (int64, error) {
		sc, ok := ScenarioByID(id)
		if !ok {
			return 0, fmt.Errorf("no scenario %q", id)
		}
		return Play(ctx, base, shopURL, sc, at)
	}
	// todo relativo a ahora (nunca en el futuro): cada noche anterior es un día antes; min separa
	// las suites de una misma noche
	at := func(d, min int) time.Time { return now.AddDate(0, 0, -d).Add(-time.Duration(60-min) * time.Minute) }

	for d := historyNights; d >= 1; d-- {
		py, js := "pytest-night", "playwright-night"
		if d == 9 || d == 3 {
			py = "pytest-nok-backend" // una caída del backend esas noches (ya resuelta)
		}
		if d == 7 || d == 2 {
			js = "playwright-flaky" // pasó al reintentar
		}
		if _, err := play(py, at(d, 10)); err != nil {
			return nil, err
		}
		if _, err := play(js, at(d, 30)); err != nil {
			return nil, err
		}
	}
	tonight := []struct {
		id  string
		min int
	}{{"rest-ok", 0}, {"rest-nok-backend", 5}, {"junit-import", 8}, {"go-suite", 14}, {"selenium-suite", 20}, {"playwright-suite", 28}, {"pytest-suite", 40}}
	for _, r := range tonight {
		id, err := play(r.id, at(0, r.min))
		if err != nil {
			return nil, err
		}
		switch r.id {
		case "pytest-suite":
			sum.Showcase = id
		case "rest-nok-backend":
			sum.APIRun = id
		}
	}

	// lo que quedó, para la guía
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/runs?limit=100", nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var runs []struct{ Total, Failed int }
	if err := json.NewDecoder(res.Body).Decode(&runs); err != nil {
		return nil, err
	}
	for _, r := range runs {
		sum.Runs++
		sum.Tests += r.Total
		sum.Failed += r.Failed
	}
	return sum, nil
}
