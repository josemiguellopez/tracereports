package db

import "testing"

// Mantenimiento: cada filtro trae solo las ejecuciones de su estado, y borrar en lote salta las que
// siguen en curso.
func TestMaintenanceRunsAndDelete(t *testing.T) {
	s := openTestStore(t)
	mk := func(name, project string) int64 {
		id, _ := s.CreateRunWithMeta(name, "qa", RunMeta{Project: project})
		tid, _ := s.CreateTest(id, "t", "", "")
		s.FinishTest(tid, "PASS", "", "")
		return id
	}
	ok := mk("ok", "shop")
	s.FinishRun(ok)
	running := mk("en curso", "shop")
	idle := mk("colgada", "crm")
	s.db.Exec(`UPDATE runs SET last_activity = 1 WHERE id = ?`, idle)
	cut := mk("ctrl-c", "shop")
	s.FinishRunWith(cut, true)
	byHand := mk("a mano", "shop")
	s.CloseRunByHand(byHand, "admin", "cancelada")
	auto := mk("sola", "crm")
	s.db.Exec(`UPDATE runs SET last_activity = 1 WHERE id = ?`, auto)
	s.CloseIfIdle(auto, NowMs(), IdleMessage("24 h"))

	ids := func(f MaintenanceFilter) []int64 {
		t.Helper()
		runs, err := s.MaintenanceRuns(f)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}
	cases := []struct {
		name string
		f    MaintenanceFilter
		want []int64
	}{
		{"todas las con problemas", MaintenanceFilter{}, []int64{auto, byHand, cut, idle, running}},
		{"sin actividad", MaintenanceFilter{State: MaintIdle, IdleBefore: NowMs() - 1000}, []int64{idle}},
		{"en curso", MaintenanceFilter{State: MaintRunning}, []int64{idle, running}},
		{"incompletas", MaintenanceFilter{State: MaintCut}, []int64{auto, byHand, cut}},
		{"cerradas a mano", MaintenanceFilter{State: MaintByHand}, []int64{byHand}},
		{"cerradas por inactividad", MaintenanceFilter{State: MaintByTimeout}, []int64{auto}},
		{"por proyecto", MaintenanceFilter{Project: "crm"}, []int64{auto, idle}},
		{"más antiguas que ahora", MaintenanceFilter{StartedBefore: 1}, []int64{}},
	}
	for _, c := range cases {
		if got := ids(c.f); !equalIDs(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if _, err := s.MaintenanceRuns(MaintenanceFilter{State: "x"}); err != ErrBadMaintenanceState {
		t.Fatalf("unknown state: %v", err)
	}

	n, _, err := s.DeleteRuns([]int64{cut, byHand, running, 99999})
	if err != nil || n != 2 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if _, err := s.GetRun(running); err != nil {
		t.Fatal("a RUNNING run is never deleted: close it first")
	}
	if _, err := s.GetRun(cut); err != ErrNotFound {
		t.Fatalf("deleted run: %v", err)
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
