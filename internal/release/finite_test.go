package release

import (
	"encoding/json"
	"testing"
)

// El umbral tiene que ser un número finito entre 0 y 100: NaN o ±Inf no se pueden comparar ni
// codificar en JSON (la decisión de release quedaba vacía).
func TestMinPassRateMustBeFinite(t *testing.T) {
	for _, bad := range []string{"NaN", "nan", "+Inf", "-Inf", "inf", "Infinity", "-0.1", "100.01", "1e3", "abc", ""} {
		if g, err := Parse("min_pass_rate=" + bad); err == nil {
			t.Errorf("min_pass_rate=%q accepted: %+v", bad, g)
		}
	}
	for v, want := range map[string]float64{"0": 0, "100": 100, "95": 95, "99.95": 99.95, "66.7": 66.7, "1e1": 10, " 80 ": 80} {
		g, err := Parse("min_pass_rate=" + v)
		if err != nil || g.MinPassRate != want {
			t.Errorf("min_pass_rate=%q: %+v %v", v, g, err)
		}
		if _, err := json.Marshal(g); err != nil {
			t.Errorf("min_pass_rate=%q cannot be encoded: %v", v, err)
		}
	}
	if g, err := Parse(""); err != nil || g.MinPassRate != 95 {
		t.Errorf("default gate: %+v %v", g, err)
	}
}
