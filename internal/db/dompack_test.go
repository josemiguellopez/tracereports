package db

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
)

// realPage builds a page shaped like the ones of a real suite (the report measured 623 nodes and 33
// icons, 475 KB in clear JSON): icons are small PNGs in base64 that hardly compress.
func realPage(title string, icons int) string {
	rng := rand.New(rand.NewSource(int64(icons)))
	glyphs := map[string]string{}
	for i := 0; i < icons; i++ {
		png := make([]byte, 1500)
		rng.Read(png)
		glyphs[fmt.Sprintf("g%d", i)] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	var nodes []map[string]any
	for i := 0; i < 600; i++ {
		nodes = append(nodes, map[string]any{"i": i, "p": i - 1, "tag": "div", "x": i % 1300, "y": i * 3, "w": 200, "h": 20, "visible": true, "depth": i % 12,
			"attrs": map[string]string{"class": fmt.Sprintf("col-sm-%d card-body px-%d", i%12, i%5)}, "text": fmt.Sprintf("%s fila %d", title, i),
			"style": map[string]string{"color": "rgb(33, 37, 41)", "fontSize": "14px", "fontFamily": "Roboto, sans-serif", "backgroundColor": "rgba(0, 0, 0, 0)"}})
	}
	out, _ := json.Marshal(map[string]any{"url": "https://app/ficha", "title": title, "viewport": map[string]int{"w": 1384, "h": 929},
		"nodes": nodes, "glyphs": glyphs})
	return string(out)
}

func storedBytes(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(LENGTH(CAST(snapshot AS BLOB))), 0) FROM (SELECT snapshot FROM test_dom UNION ALL SELECT snapshot FROM test_dom_steps)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var g int64
	s.db.QueryRow(`SELECT COALESCE(SUM(bytes), 0) FROM dom_glyphs`).Scan(&g)
	return n + g
}

// Las páginas se guardan comprimidas y con los íconos una sola vez, y se leen idénticas. Con
// DOM_STEPS=on-problem una suite completa llegaba a ~1 GB por corrida.
func TestDOMPagesArePackedAndReadBackTheSame(t *testing.T) {
	s := openTestStore(t)
	runID, _ := s.CreateRun("Clínico", "qa")
	a, _ := s.CreateTest(runID, "test_a", "", "")
	b, _ := s.CreateTest(runID, "test_b", "", "")
	var clear int64
	pages := map[string]string{}
	for i := 1; i <= 10; i++ {
		p := realPage(fmt.Sprintf("paso %d", i), 33)
		clear += int64(len(p))
		seq, err := s.SaveDOMStep(a, fmt.Sprintf("paso %d", i), 0, p)
		if err != nil || seq != i {
			t.Fatal(seq, err)
		}
		pages[fmt.Sprint(i)] = p
	}
	fail := realPage("fallo", 33)
	clear += int64(len(fail))
	if err := s.SaveDOM(b, fail); err != nil {
		t.Fatal(err)
	}
	stored := storedBytes(t, s)
	if stored*8 > clear {
		t.Fatalf("11 pages: %d KB in clear, %d KB stored (want at least 8× less)", clear/1024, stored/1024)
	}
	t.Logf("11 pages: %d KB in clear → %d KB stored", clear/1024, stored/1024)
	for i := 1; i <= 10; i++ {
		got, err := s.GetDOMStep(a, i)
		if err != nil {
			t.Fatal(err)
		}
		assertSamePage(t, pages[fmt.Sprint(i)], got)
	}
	got, err := s.GetDOM(b)
	if err != nil {
		t.Fatal(err)
	}
	assertSamePage(t, fail, got)
	var icons int
	s.db.QueryRow(`SELECT COUNT(*) FROM dom_glyphs`).Scan(&icons)
	if icons != 33 {
		t.Fatalf("each icon once for the whole suite: %d", icons)
	}
}

func assertSamePage(t *testing.T, want, got string) {
	t.Helper()
	var a, b any
	if json.Unmarshal([]byte(want), &a) != nil || json.Unmarshal([]byte(got), &b) != nil {
		t.Fatalf("not JSON: %.80s", got)
	}
	wa, _ := json.Marshal(a)
	wb, _ := json.Marshal(b)
	if !bytes.Equal(wa, wb) {
		t.Fatalf("the page does not read back the same")
	}
}

// Con el cifrado en reposo la página se comprime antes de sellarla (sellada no se comprime), y la
// rotación de llaves la vuelve a sellar sin tocarla.
func TestPackedPagesAreSealedAndResealed(t *testing.T) {
	s := openTestStore(t)
	k1, _ := atrest.New(bytes.Repeat([]byte{1}, 32), nil, true)
	s.SetVault(k1)
	runID, _ := s.CreateRun("Clínico", "qa")
	id, _ := s.CreateTest(runID, "test_a", "", "")
	page := realPage("Paciente RUT 13.905.701-5", 5)
	if err := s.SaveDOM(id, page); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDOMStep(id, "paso", 0, page); err != nil {
		t.Fatal(err)
	}
	var raw string
	s.db.QueryRow(`SELECT snapshot FROM test_dom WHERE test_id=?`, id).Scan(&raw)
	if !atrest.Sealed(raw) || strings.Contains(raw, "Paciente") || int64(len(raw))*4 > int64(len(page)) {
		t.Fatalf("sealed and compressed: %d of %d bytes, sealed=%v", len(raw), len(page), atrest.Sealed(raw))
	}
	k2, _ := atrest.New(bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{1}, 32), true)
	s.SetVault(k2)
	if changed, unreadable, err := s.ResealDOM(k2); err != nil || changed != 2 || unreadable != 0 {
		t.Fatal(changed, unreadable, err)
	}
	for _, read := range []func() (string, error){func() (string, error) { return s.GetDOM(id) }, func() (string, error) { return s.GetDOMStep(id, 1) }} {
		got, err := read()
		if err != nil {
			t.Fatal(err)
		}
		assertSamePage(t, page, got)
	}
}

// Las páginas de una versión anterior (JSON en claro o sellado, con sus íconos adentro) se leen igual, y
// «Compactar ahora» las empaqueta.
func TestOlderPagesAreReadAndPackedOnCompaction(t *testing.T) {
	s := openTestStore(t)
	runID, _ := s.CreateRun("Clínico", "qa")
	id, _ := s.CreateTest(runID, "test_a", "", "")
	page := realPage("antes", 20)
	s.db.Exec(`INSERT INTO test_dom(test_id, snapshot, created_at) VALUES(?,?,?)`, id, page, NowMs())
	for i := 1; i <= 3; i++ {
		s.db.Exec(`INSERT INTO test_dom_steps(test_id, seq, label, at, snapshot, created_at, kind) VALUES(?,?,?,?,?,?,'step')`, id, i, "", 0, page, NowMs())
	}
	got, err := s.GetDOMStep(id, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertSamePage(t, page, got)
	before := storedBytes(t, s)
	n, err := s.PackDOM(0)
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if again, _ := s.PackDOM(0); again != 0 {
		t.Fatalf("a packed page is not packed twice: %d", again)
	}
	after := storedBytes(t, s)
	if after*3 > before {
		t.Fatalf("packing: %d → %d bytes", before, after)
	}
	got, err = s.GetDOM(id)
	if err != nil {
		t.Fatal(err)
	}
	assertSamePage(t, page, got)
}

// Un ícono que ningún test usa se borra con la retención; uno que otro test usa queda.
func TestSharedIconsArePrunedWithTheirRuns(t *testing.T) {
	s := openTestStore(t)
	old, _ := s.CreateRun("vieja", "qa")
	keep, _ := s.CreateRun("nueva", "qa")
	a, _ := s.CreateTest(old, "test_a", "", "")
	b, _ := s.CreateTest(keep, "test_b", "", "")
	s.SaveDOM(a, realPage("a", 3))                                     // íconos g0..g2
	s.SaveDOM(b, strings.Replace(realPage("b", 1), `"g0"`, `"g9"`, 1)) // otro ícono
	s.FinishRun(old)
	if n, _, err := s.DeleteRuns([]int64{old}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var icons int
	s.db.QueryRow(`SELECT COUNT(*) FROM dom_glyphs`).Scan(&icons)
	if icons != 1 {
		t.Fatalf("only the icon the other run uses stays: %d", icons)
	}
	got, err := s.GetDOM(b)
	if err != nil || !strings.Contains(got, `"g9":"data:image/png;base64,`) {
		t.Fatalf("the remaining run still has its icon: %v %.200s", err, got)
	}
}

func TestDOMSizesByRun(t *testing.T) {
	s := openTestStore(t)
	r1, _ := s.CreateRun("liviana", "qa")
	r2, _ := s.CreateRun("pesada", "qa")
	a, _ := s.CreateTest(r1, "a", "", "")
	b, _ := s.CreateTest(r2, "b", "", "")
	s.SaveDOM(a, realPage("a", 0))
	for i := 0; i < 5; i++ {
		s.SaveDOMStep(b, "", 0, realPage(fmt.Sprint("b", i), 0))
	}
	total, _, pages, runs, err := s.DOMSizes(10)
	if err != nil || pages != 6 || len(runs) != 2 || runs[0].Name != "pesada" || runs[0].Pages != 5 || total != runs[0].Bytes+runs[1].Bytes {
		t.Fatalf("%d %d %+v %v", total, pages, runs, err)
	}
	one, err := s.RunDOMSize(r2)
	if err != nil || one.Pages != 5 || one.Bytes != runs[0].Bytes {
		t.Fatalf("%+v %v", one, err)
	}
	if _, err := s.RunDOMSize(999); err != ErrNotFound {
		t.Fatalf("missing run: %v", err)
	}
}
