package release

import "testing"

func manyTests(pass, fail int) []string {
	out := make([]string, 0, pass+fail)
	for i := 0; i < pass; i++ {
		out = append(out, "PASS")
	}
	for i := 0; i < fail; i++ {
		out = append(out, "FAIL")
	}
	return out
}

// La decisión usa la tasa real; el redondeo es solo para mostrarla.
func TestPassRateThresholdUsesTheExactRate(t *testing.T) {
	cases := []struct {
		pass, fail int
		min        float64
		ok         bool
		shown      float64
	}{
		{379, 20, 95, false, 95.0},    // 94,98746… se muestra 95,0 pero no llega al mínimo
		{380, 20, 95, true, 95.0},     // exactamente 95 %
		{381, 20, 95, true, 95.0},     // 95,0125 %
		{19, 1, 95, true, 95.0},       // 95 % exacto con pocos tests
		{949, 51, 95, false, 94.9},    // 94,9 %
		{2, 1, 66.7, false, 66.7},     // 66,666… < 66,7 aunque se muestre 66,7
		{1999, 1, 99.95, true, 100.0}, // 99,95 % exacto: cumple
	}
	for _, c := range cases {
		var tests []string
		tests = append(tests, manyTests(c.pass, c.fail)...)
		d := run()
		for i, st := range tests {
			d.Tests = append(d.Tests, test("t"+itoa(i), st, ""))
		}
		g := Default()
		g.MinPassRate = c.min
		got := Evaluate(g, d, nil)
		if check(got, "pass_rate").OK != c.ok {
			t.Errorf("%d/%d with min %.2f: ok=%v, want %v (shown %.1f)", c.pass, c.fail, c.min, !c.ok, c.ok, got.PassRate)
		}
		if got.PassRate != c.shown {
			t.Errorf("%d/%d: shown %.2f, want %.1f (rounded only for display)", c.pass, c.fail, got.PassRate, c.shown)
		}
	}
}

func itoa(i int) string {
	const d = "0123456789"
	if i < 10 {
		return d[i : i+1]
	}
	return itoa(i/10) + d[i%10:i%10+1]
}
