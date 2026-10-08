package db

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// awkwardDirs are directory names that mean something in a file: URI.
func awkwardDirs() []string {
	dirs := []string{"audit#folder", "pct%25dir", "pct%zz", "with space", "ñandú ✓", "a#b%23c d"}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, "q?mark", `back\slash`)
	}
	return dirs
}

// filesUnder lists every file under root (relative, slash separated).
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// onlyDBFiles fails if root holds anything but db (and its -wal/-shm).
func onlyDBFiles(t *testing.T, root, db string) {
	t.Helper()
	rel, _ := filepath.Rel(root, db)
	rel = filepath.ToSlash(rel)
	for _, f := range filesUnder(t, root) {
		if f != rel && f != rel+"-wal" && f != rel+"-shm" && f != rel+"-journal" {
			t.Fatalf("unexpected file %q (want only %q): %v", f, rel, filesUnder(t, root))
		}
	}
}

// Open crea y usa la base exactamente en la ruta pedida, con sus pragmas, aunque la ruta tenga
// caracteres especiales de una URI.
func TestOpenUsesTheExactPath(t *testing.T) {
	for _, dir := range awkwardDirs() {
		root := t.TempDir()
		path := filepath.Join(root, dir, "trace.db")
		s, err := Open(path)
		if err != nil {
			t.Fatalf("%q: %v", dir, err)
		}
		run, _ := s.CreateRun("r", "qa")
		var fk, timeout int
		var mode string
		s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
		s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout)
		s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
		s.Close()
		if fk != 1 || timeout != 5000 || mode != "wal" {
			t.Fatalf("%q: pragmas fk=%d timeout=%d mode=%s", dir, fk, timeout, mode)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%q: database not at the requested path: %v", dir, err)
		}
		onlyDBFiles(t, root, path)
		// reabrir encuentra lo mismo
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if r, err := s.GetRun(run); err != nil || r.Name != "r" {
			t.Fatalf("%q: reopen: %v %v", dir, r, err)
		}
		s.Close()
	}
}

// Una ruta relativa con caracteres especiales también.
func TestOpenRelativePath(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	s, err := Open(filepath.Join("rel#dir", "x y.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	onlyDBFiles(t, root, filepath.Join(root, "rel#dir", "x y.db"))
}

// OpenExisting en solo lectura abre esa base (no otra), no crea nada y no deja escribir.
func TestOpenExistingReadOnlyUsesTheExactPath(t *testing.T) {
	for _, dir := range awkwardDirs() {
		root := t.TempDir()
		path := filepath.Join(root, dir, "proper.db")
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.CreateRun("the right one", "qa")
		s.Close()

		ro, err := OpenExisting(path, true)
		if err != nil {
			t.Fatalf("%q: %v", dir, err)
		}
		var name string
		if err := ro.db.QueryRow(`SELECT name FROM runs`).Scan(&name); err != nil || name != "the right one" {
			t.Fatalf("%q: opened another database: %q %v", dir, name, err)
		}
		if _, err := ro.db.Exec(`INSERT INTO runs(name, environment, status, started_at) VALUES ('x','','PASS',1)`); err == nil {
			t.Fatalf("%q: read-only must not write", dir)
		}
		ro.Close()
		// solo la base pedida y sus archivos de WAL (SQLite los usa para leer una base en modo WAL)
		onlyDBFiles(t, root, path)
		// lectura y escritura: en la misma base
		rw, err := OpenExisting(path, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rw.CreateRun("second", "qa"); err != nil {
			t.Fatal(err)
		}
		var n int
		rw.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
		rw.Close()
		if n != 2 {
			t.Fatalf("%q: rw wrote elsewhere: %d", dir, n)
		}
	}
}

// Sin base en la ruta, OpenExisting falla y no crea nada.
func TestOpenExistingMissingCreatesNothing(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a#b"), 0o755)
	for _, ro := range []bool{true, false} {
		if _, err := OpenExisting(filepath.Join(root, "a#b", "none.db"), ro); err == nil {
			t.Fatal("must fail")
		}
	}
	if f := filesUnder(t, root); len(f) != 0 {
		t.Fatalf("created: %v", f)
	}
}

// La URI se arma para rutas Windows y Unix (sin depender del sistema donde corre la prueba).
func TestFileURI(t *testing.T) {
	cases := map[string]string{
		"/home/ana/data/trace.db":       "file:///home/ana/data/trace.db",
		"/srv/a b/x#y?z%25.db":          "file:///srv/a%20b/x%23y%3Fz%2525.db",
		"C:/Users/Ana/audit#1/trace.db": "file:///C:/Users/Ana/audit%231/trace.db",
		"//server/share/trace.db":       "file:////server/share/trace.db",
		"/tmp/ñ.db":                     "file:///tmp/%C3%B1.db",
	}
	for in, want := range cases {
		if got := fileURI(in); got != want {
			t.Errorf("fileURI(%q) = %q, want %q", in, got, want)
		}
	}
}

// Dónde abrían las versiones anteriores una ruta afectada: la ruta cortada en el primer # o ?,
// con las secuencias %XX decodificadas.
func TestLegacyLocation(t *testing.T) {
	cases := map[string]string{
		"/data/trace.db":         "",
		"/data#1/trace.db":       "/data",
		"/data/x?y/trace.db":     "/data/x",
		"/data/50%25/trace.db":   "/data/50%/trace.db",
		"/data/a%20b/trace.db":   "/data/a b/trace.db",
		"C:/tr#prod/trace.db":    "C:/tr",
		"/data/plain%zz/trac.db": "",
	}
	for in, want := range cases {
		got := legacyLocation(filepath.FromSlash(in))
		if filepath.ToSlash(got) != want {
			t.Errorf("legacyLocation(%q) = %q, want %q", in, got, want)
		}
	}
}
