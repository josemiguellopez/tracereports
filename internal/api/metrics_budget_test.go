package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Presupuesto de las métricas: un rango propio cubre como máximo db.MaxMetricsDays fechas (366,
// un año con 29 de febrero). Uno más largo se rechaza con un 400 que lo explica, antes de armar
// el gráfico; nunca se recorta en silencio. Dentro del presupuesto, los KPIs cuentan todo y el
// gráfico tiene un punto por fecha.
func TestCustomRangeBudget(t *testing.T) {
	loc := inZone(t, "Europe/Madrid")
	srv, _ := newTestServer(t)
	finishedRunAt(t, srv, "s", time.Date(2024, 1, 1, 0, 0, 0, 0, loc), "FAIL")
	finishedRunAt(t, srv, "s", time.Date(2024, 12, 31, 23, 59, 0, 0, loc), "PASS")

	// rango habitual
	m := metricsFor(t, srv, "from=2024-06-01&to=2024-06-30&suite=s")
	if len(m.Daily) != 30 || m.Current.Tests != 0 {
		t.Fatalf("a month: %d points, %+v", len(m.Daily), m.Current)
	}
	// el máximo: todo 2024 (bisiesto, 366 fechas), con un resultado en cada borde
	m = metricsFor(t, srv, "from=2024-01-01&to=2024-12-31&suite=s")
	if len(m.Daily) != db.MaxMetricsDays || m.Current.Tests != 2 || m.Daily[0].Failed != 1 || m.Daily[365].Passed != 1 {
		t.Fatalf("a whole leap year: %d points, %+v", len(m.Daily), m.Current)
	}
	// uno más: rechazado y explicado
	for _, q := range []string{"from=2024-01-01&to=2025-01-01", "from=2000-01-01&to=2100-01-01", "from=0001-01-01&to=9999-12-31"} {
		rec := call(t, srv, "GET", "/api/v1/metrics?"+q, "")
		var out struct{ Error string }
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 400 || !strings.Contains(out.Error, "366") {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
		if len(rec.Body.Bytes()) > 300 {
			t.Fatalf("%s: the rejection is short: %d bytes", q, len(rec.Body.Bytes()))
		}
	}
}
