package db

import (
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Mientras la retención trabaja, otro proceso (otra conexión) cierra una ejecución vieja y agrega
// una captura a una ejecución elegida. Cada ejecución borrada tiene que devolver todos sus archivos.
func TestPurgeReturnsEveryFileOfTheRunsItDeletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)") // como toda conexión del producto
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	mk := func(name string, closed bool, shot string) (runID, testID int64) {
		runID, _ = s.CreateRun(name, "")
		testID, _ = s.CreateTest(runID, "t", "", "")
		if shot != "" {
			s.AddLog(testID, "INFO", "shot", 0, shot)
		}
		s.FinishTest(testID, "PASS", "", "")
		if closed {
			s.FinishRun(runID)
		}
		return
	}
	oldClosed, oldTest := mk("old-closed", true, "/screenshots/old-closed.png")
	oldOpen, _ := mk("old-open", false, "/screenshots/old-open.png")
	recent, _ := mk("recent", true, "/screenshots/recent.png")
	s.db.Exec(`UPDATE runs SET started_at = 1000 WHERE id IN (?, ?)`, oldClosed, oldOpen)

	// entre elegir los archivos y borrar, otra conexión cierra la vieja abierta y agrega evidencia
	done := make(chan struct{})
	var lateErr error
	purgeHook = func() {
		go func() {
			defer close(done)
			other.Exec(`UPDATE runs SET status = 'PASS', ended_at = 2000 WHERE id = ?`, oldOpen)
			// si llega después del borrado, la clave foránea lo rechaza: no queda evidencia suelta
			_, lateErr = other.Exec(`INSERT INTO logs(test_id, status, message, timestamp, screenshot) VALUES (?, 'INFO', 'late', 3000, '/screenshots/late.png')`, oldTest)
		}()
		time.Sleep(150 * time.Millisecond) // con el arreglo, esas escrituras esperan a que termine la purga
	}
	defer func() { purgeHook = nil }()

	n, files, err := s.PurgeRunsBefore(time.Now().Add(-time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	<-done
	purgeHook = nil

	// lo que quedó en la base no tiene archivos fuera de lo devuelto: o se borró con su archivo,
	// o sigue ahí (y una pasada siguiente lo verá)
	var remaining []int64
	rows, _ := s.db.Query(`SELECT id FROM runs ORDER BY id`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		remaining = append(remaining, id)
	}
	rows.Close()
	deleted := map[int64]bool{oldClosed: true, oldOpen: true, recent: true}
	for _, id := range remaining {
		delete(deleted, id)
	}
	sort.Strings(files)
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	want := map[int64][]string{oldClosed: {"/screenshots/old-closed.png"}, oldOpen: {"/screenshots/old-open.png"}}
	if lateErr == nil && deleted[oldClosed] {
		// la captura tardía entró antes del borrado: era parte de la ejecución borrada
		want[oldClosed] = append(want[oldClosed], "/screenshots/late.png")
	}
	for id := range deleted {
		for _, f := range want[id] {
			if !got[f] {
				t.Errorf("run %d was deleted but its file %s was not returned (deleted=%d files=%v remaining=%v)", id, f, n, files, remaining)
			}
		}
	}
	if deleted[recent] {
		t.Error("a recent run must be kept")
	}
	if !deleted[oldClosed] {
		t.Error("the old closed run is deleted")
	}
	// y las referencias que quedan en la base apuntan a ejecuciones que siguen ahí
	var orphans int
	s.db.QueryRow(`SELECT COUNT(*) FROM logs l LEFT JOIN tests t ON t.id = l.test_id WHERE t.id IS NULL`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d logs left without their test", orphans)
	}
	if n != len(deleted) {
		t.Errorf("deleted count %d, but %d runs are gone", n, len(deleted))
	}
}

func TestPurgeKeepsRunningAndRecentRuns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	running, _ := s.CreateRun("running", "")
	closed, _ := s.CreateRun("closed", "")
	tid, _ := s.CreateTest(closed, "t", "", "")
	s.AddLog(tid, "INFO", "x", 0, "/screenshots/a.png")
	s.AddArtifact(&Artifact{TestID: tid, Kind: "trace", Name: "t.zip", URL: "/screenshots/t.zip", Size: 1})
	s.FinishRun(closed)
	s.db.Exec(`UPDATE runs SET started_at = 1000`)
	n, files, err := s.PurgeRunsBefore(2000)
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	sort.Strings(files)
	if len(files) != 2 || files[0] != "/screenshots/a.png" || files[1] != "/screenshots/t.zip" {
		t.Fatalf("files: %v", files)
	}
	if r, err := s.GetRun(running); err != nil || r.Status != "RUNNING" {
		t.Fatal("a running run is never purged")
	}
}
