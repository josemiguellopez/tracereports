package db

import "testing"

// Un test que el runner cierra en PASS pero tiene un paso en WARNING se guarda como WARNING:
// la advertencia no queda escondida dentro de un test verde.
func TestFinishPassWithWarningStepIsWarning(t *testing.T) {
	s := openTestStore(t)
	run, _ := s.CreateRun("suite", "")
	cases := []struct {
		name, finish string
		steps        []string
		want         string
		attempts     int
	}{
		{"pass after retry", "PASS", []string{"WARNING", "PASS"}, "PASS", 2},
		{"pass with warning", "PASS", []string{"PASS", "WARNING", "PASS"}, "WARNING", 1},
		{"plain pass", "PASS", []string{"PASS", "INFO"}, "PASS", 1},
		{"derived warning", "", []string{"PASS", "WARNING"}, "WARNING", 1},
		{"derived fail wins", "", []string{"WARNING", "FAIL"}, "FAIL", 1},
		{"explicit fail", "FAIL", []string{"WARNING"}, "FAIL", 1},
		{"explicit skip", "SKIP", []string{"WARNING"}, "SKIP", 1},
	}
	for _, c := range cases {
		id, err := s.CreateTest(run, c.name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if c.attempts > 1 {
			if err := s.SetAttempts(id, c.attempts); err != nil {
				t.Fatal(err)
			}
		}
		for _, st := range c.steps {
			if _, err := s.AddLog(id, st, "step", 0, ""); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.FinishTest(id, c.finish, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != c.want {
			t.Errorf("%s: status %q, want %q", c.name, got.Status, c.want)
		}
	}
}
