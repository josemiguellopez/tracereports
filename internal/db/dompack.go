package db

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// Las páginas del Inspector (test_dom y test_dom_steps) se guardan empaquetadas:
//
//   - los íconos (glyphs: PNG chicos en base64, que casi no se comprimen) van una sola vez por su
//     SHA-256 en dom_glyphs: son los mismos en cada paso de un test y en cada test de la suite. La
//     página guarda "trg:sha256:<hash>" en su lugar. Como el CSS del modo HTML, un ícono no es un dato
//     de la página (no se sella) y se borra con la retención cuando ningún test lo usa;
//   - el JSON que queda va comprimido (gzip), y después se sella si el cifrado en reposo está activo.
//
// Una página guardada antes (JSON en claro, con sus íconos adentro) se lee igual; «Compactar ahora»
// las empaqueta (PackDOM).
const domGlyphSchema = `
CREATE TABLE IF NOT EXISTS dom_glyphs (
	hash       TEXT    PRIMARY KEY,
	data       TEXT    NOT NULL,
	bytes      INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS dom_glyph_refs (
	test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	hash    TEXT    NOT NULL,
	PRIMARY KEY (test_id, hash)
);
CREATE INDEX IF NOT EXISTS idx_dom_glyph_refs_hash ON dom_glyph_refs(hash);
-- lo que ocupa cada página guardada (seq 0 = la del fallo), anotado al guardarla: medir un run o la base
-- nunca vuelve a leer las páginas (sumar sobre ellas copiaba la base entera a una tabla temporal)
CREATE TABLE IF NOT EXISTS dom_sizes (
	test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	seq     INTEGER NOT NULL,
	bytes   INTEGER NOT NULL,
	PRIMARY KEY (test_id, seq)
);
`

// glyphRef replaces a stored icon in a page; minGlyph: smaller values stay in the page.
const (
	glyphRef = "trg:sha256:"
	minGlyph = 64
)

// gzipMagic starts a compressed page (a JSON page starts with "{").
var gzipMagic = []byte{0x1f, 0x8b}

// packDOM turns a page's JSON into what is stored: icons by reference, compressed, sealed with name
// when encrypting. The result is a string when sealed and bytes (a BLOB) otherwise.
func (s *Store) packDOM(testID int64, name, snapshotJSON string) (any, error) {
	page, err := s.shareGlyphs(testID, snapshotJSON)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if _, err := zw.Write([]byte(page)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if s.vault.Encrypting() {
		return s.vault.Seal(name, buf.String())
	}
	return buf.Bytes(), nil
}

// storedLen is the size of a stored page value (packDOM returns bytes or a sealed string).
func storedLen(v any) int {
	switch x := v.(type) {
	case []byte:
		return len(x)
	case string:
		return len(x)
	}
	return 0
}

// noteDOMSize records what a stored page takes (seq 0: the failure page).
func (s *Store) noteDOMSize(testID int64, seq int, v any) error {
	_, err := s.db.Exec(`INSERT INTO dom_sizes(test_id, seq, bytes) VALUES(?,?,?) ON CONFLICT(test_id, seq) DO UPDATE SET bytes=excluded.bytes`,
		testID, seq, storedLen(v))
	return err
}

// MeasureCursor is where the measuring of the pages stored before dom_sizes existed goes on (by primary
// key: no page is looked at twice).
type MeasureCursor struct {
	DOMTest  int64 // la última página del fallo medida (test_id)
	StepTest int64 // la última página de paso medida (test_id, seq)
	StepSeq  int
	DOMDone  bool
	StepDone bool
}

// Done says every page stored before is measured.
func (c MeasureCursor) Done() bool { return c.DOMDone && c.StepDone }

// MeasureDOMSizes measures the next batch of pages after cur and records their sizes. It reads on the
// read-only connections, by primary key and with octet_length (the size kept in the record: the page
// itself is never loaded), and writes the batch in one short transaction of values already computed:
// the connection that writes is held for milliseconds. It returns the cursor to go on from and how many
// pages it looked at.
func (s *Store) MeasureDOMSizes(cur MeasureCursor, batch int) (MeasureCursor, int, error) {
	type size struct {
		test  int64
		seq   int
		bytes int64
	}
	var sizes []size
	if !cur.DOMDone {
		rows, err := s.reader().Query(`SELECT test_id, octet_length(snapshot) FROM test_dom WHERE test_id > ? ORDER BY test_id LIMIT ?`, cur.DOMTest, batch)
		if err != nil {
			return cur, 0, err
		}
		n := 0
		for rows.Next() {
			var z size
			if err := rows.Scan(&z.test, &z.bytes); err != nil {
				rows.Close()
				return cur, 0, err
			}
			sizes = append(sizes, z)
			cur.DOMTest = z.test
			n++
		}
		rows.Close()
		cur.DOMDone = n < batch
	}
	if !cur.StepDone && len(sizes) < batch {
		rows, err := s.reader().Query(`SELECT test_id, seq, octet_length(snapshot) FROM test_dom_steps WHERE (test_id, seq) > (?, ?)
			ORDER BY test_id, seq LIMIT ?`, cur.StepTest, cur.StepSeq, batch-len(sizes))
		if err != nil {
			return cur, 0, err
		}
		want, n := batch-len(sizes), 0
		for rows.Next() {
			var z size
			if err := rows.Scan(&z.test, &z.seq, &z.bytes); err != nil {
				rows.Close()
				return cur, 0, err
			}
			sizes = append(sizes, z)
			cur.StepTest, cur.StepSeq = z.test, z.seq
			n++
		}
		rows.Close()
		cur.StepDone = n < want
	}
	if len(sizes) == 0 {
		return cur, 0, nil
	}
	q := `INSERT OR IGNORE INTO dom_sizes(test_id, seq, bytes) VALUES ` + strings.TrimSuffix(strings.Repeat("(?,?,?),", len(sizes)), ",")
	args := make([]any, 0, 3*len(sizes))
	for _, z := range sizes {
		args = append(args, z.test, z.seq, z.bytes)
	}
	if _, err := s.db.Exec(q, args...); err != nil { // una sola escritura corta, sin leer nada
		return cur, 0, err
	}
	return cur, len(sizes), nil
}

// ForgetDOMSizes deletes the recorded sizes of every page (TRACEREPORTS_REMEASURE_PAGES: measure them
// all again on startup).
func (s *Store) ForgetDOMSizes() error {
	_, err := s.db.Exec(`DELETE FROM dom_sizes`)
	return err
}

// unpackDOM is the inverse of packDOM for what the vault already opened: it decompresses a packed page
// and puts its icons back. A page stored before packing comes out as it was.
func (s *Store) unpackDOM(opened string) (string, error) {
	if strings.HasPrefix(opened, string(gzipMagic)) {
		zr, err := gzip.NewReader(strings.NewReader(opened))
		if err != nil {
			return "", err
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			return "", err
		}
		opened = string(raw)
	}
	if !strings.Contains(opened, glyphRef) {
		return opened, nil
	}
	return s.restoreGlyphs(opened)
}

// shareGlyphs stores the page's icons in dom_glyphs (each one once) and returns the page with their
// references. A page without icons (or not a JSON object) is returned as is.
func (s *Store) shareGlyphs(testID int64, snapshotJSON string) (string, error) {
	if !strings.Contains(snapshotJSON, `"glyphs"`) {
		return snapshotJSON, nil
	}
	var page map[string]json.RawMessage
	if json.Unmarshal([]byte(snapshotJSON), &page) != nil {
		return snapshotJSON, nil
	}
	var glyphs map[string]string
	if raw, ok := page["glyphs"]; !ok || json.Unmarshal(raw, &glyphs) != nil || len(glyphs) == 0 {
		return snapshotJSON, nil
	}
	now := NowMs()
	for k, v := range glyphs {
		if len(v) < minGlyph || strings.HasPrefix(v, glyphRef) {
			continue
		}
		sum := sha256.Sum256([]byte(v))
		hash := hex.EncodeToString(sum[:])
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO dom_glyphs(hash, data, bytes, created_at) VALUES(?,?,?,?)`, hash, v, len(v), now); err != nil {
			return "", err
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO dom_glyph_refs(test_id, hash) VALUES(?,?)`, testID, hash); err != nil {
			return "", err
		}
		glyphs[k] = glyphRef + hash
	}
	raw, err := json.Marshal(glyphs)
	if err != nil {
		return "", err
	}
	page["glyphs"] = raw
	out, err := json.Marshal(page)
	return string(out), err
}

// restoreGlyphs puts the stored icons back in a page (an icon no longer stored is left out: the
// Inspector draws the node without it).
func (s *Store) restoreGlyphs(pageJSON string) (string, error) {
	var page map[string]json.RawMessage
	if json.Unmarshal([]byte(pageJSON), &page) != nil {
		return pageJSON, nil
	}
	var glyphs map[string]string
	if raw, ok := page["glyphs"]; !ok || json.Unmarshal(raw, &glyphs) != nil {
		return pageJSON, nil
	}
	for k, v := range glyphs {
		hash, ok := strings.CutPrefix(v, glyphRef)
		if !ok {
			continue
		}
		var data string
		err := s.db.QueryRow(`SELECT data FROM dom_glyphs WHERE hash=?`, hash).Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			delete(glyphs, k)
			continue
		}
		if err != nil {
			return "", err
		}
		glyphs[k] = data
	}
	raw, err := json.Marshal(glyphs)
	if err != nil {
		return "", err
	}
	page["glyphs"] = raw
	out, err := json.Marshal(page)
	return string(out), err
}

// pruneDOMGlyphs deletes the icons no test uses any more (after deleting runs).
func (s *Store) pruneDOMGlyphs() error {
	_, err := s.db.Exec(`DELETE FROM dom_glyphs WHERE NOT EXISTS (SELECT 1 FROM dom_glyph_refs r WHERE r.hash = dom_glyphs.hash)`)
	return err
}

// PackDOM packs the pages stored before packing existed (in clear JSON or sealed JSON), at most limit
// of them (0 = all). It returns how many it packed; a page sealed with a key that is not configured
// stays as it is. Run before compacting, so the space goes back to the disk.
func (s *Store) PackDOM(limit int) (int, error) {
	type row struct {
		table string
		test  int64
		seq   int
	}
	var todo []row
	collect := func(q, table string) error {
		rows, err := s.db.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r := row{table: table}
			if err := rows.Scan(&r.test, &r.seq); err != nil {
				return err
			}
			todo = append(todo, r)
		}
		return rows.Err()
	}
	// en claro: empieza con "{"; sellada: hay que abrirla para saber (se mira al empaquetarla)
	if err := collect(`SELECT test_id, 0 FROM test_dom WHERE substr(CAST(snapshot AS BLOB), 1, 1) != x'1f' ORDER BY test_id`, "test_dom"); err != nil {
		return 0, err
	}
	if ok, err := s.HasTable("test_dom_steps"); err != nil {
		return 0, err
	} else if ok {
		if err := collect(`SELECT test_id, seq FROM test_dom_steps WHERE substr(CAST(snapshot AS BLOB), 1, 1) != x'1f' ORDER BY test_id, seq`, "test_dom_steps"); err != nil {
			return 0, err
		}
	}
	packed := 0
	for _, r := range todo {
		if limit > 0 && packed >= limit {
			break
		}
		name, where, args := domName(r.test), `test_id=?`, []any{r.test}
		if r.table == "test_dom_steps" {
			name, where, args = domStepName(r.test, r.seq), `test_id=? AND seq=?`, []any{r.test, r.seq}
		}
		var stored string
		if err := s.db.QueryRow(`SELECT snapshot FROM `+r.table+` WHERE `+where, args...).Scan(&stored); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // la borró la retención mientras tanto
			}
			return packed, err
		}
		plain, err := s.vault.Open(name, stored)
		if err != nil || strings.HasPrefix(plain, string(gzipMagic)) || !strings.HasPrefix(strings.TrimSpace(plain), "{") {
			continue // sellada con otra llave, o ya empaquetada
		}
		value, err := s.packDOM(r.test, name, plain)
		if err != nil {
			return packed, err
		}
		if _, err := s.db.Exec(`UPDATE `+r.table+` SET snapshot=? WHERE `+where, append([]any{value}, args...)...); err != nil {
			return packed, err
		}
		if err := s.noteDOMSize(r.test, r.seq, value); err != nil {
			return packed, err
		}
		packed++
		if packed%packBatch == 0 {
			time.Sleep(packPause) // cada página es una escritura corta; entre lotes, la ingesta y las lecturas pasan
		}
	}
	return packed, nil
}

// DOMRunSize is what the Inspector's pages of one run take in the database.
type DOMRunSize struct {
	RunID int64  `json:"run_id"`
	Name  string `json:"name"`
	Pages int    `json:"pages"`
	Bytes int64  `json:"bytes"`
}

// DOMSizes returns what the Inspector's pages take: in total, the shared icons, and the heaviest
// runs (at most top), as stored (packed or not).
func (s *Store) DOMSizes(top int) (total, glyphs int64, pages int, runs []DOMRunSize, err error) {
	q := `SELECT t.run_id, r.name, COUNT(*), COALESCE(SUM(z.bytes), 0) FROM dom_sizes z
		JOIN tests t ON t.id = z.test_id JOIN runs r ON r.id = t.run_id GROUP BY t.run_id ORDER BY 4 DESC`
	rows, err := s.reader().Query(q)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	defer rows.Close()
	runs = []DOMRunSize{}
	for rows.Next() {
		var r DOMRunSize
		if err := rows.Scan(&r.RunID, &r.Name, &r.Pages, &r.Bytes); err != nil {
			return 0, 0, 0, nil, err
		}
		total += r.Bytes
		pages += r.Pages
		if len(runs) < top {
			runs = append(runs, r)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, nil, err
	}
	err = s.reader().QueryRow(`SELECT COALESCE(SUM(bytes), 0) FROM dom_glyphs`).Scan(&glyphs)
	return total, glyphs, pages, runs, err
}

// runDOMSizeQuery reads only dom_sizes (by the index of tests.run_id), never the pages.
const runDOMSizeQuery = `SELECT r.name, COUNT(z.test_id), COALESCE(SUM(z.bytes), 0) FROM runs r
	LEFT JOIN tests t ON t.run_id = r.id LEFT JOIN dom_sizes z ON z.test_id = t.id
	WHERE r.id = ? GROUP BY r.id`

// PackDOM pauses packPause every packBatch pages.
const (
	packBatch = 50
	packPause = 25 * time.Millisecond
)

// RunDOMSize is what the Inspector's pages of one run take (pages and bytes as stored).
func (s *Store) RunDOMSize(runID int64) (DOMRunSize, error) {
	r := DOMRunSize{RunID: runID}
	err := s.reader().QueryRow(runDOMSizeQuery, runID).Scan(&r.Name, &r.Pages, &r.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// MeasureAllDOMSizes measures every page stored before dom_sizes existed, batch by batch, resting
// between batches max(minPause, 3 × what the batch took): measuring never takes more than about a
// quarter of the time, so ingestion and the UI go on. progress gets the pages looked at so far (every
// batch); it returns the total.
func (s *Store) MeasureAllDOMSizes(batch int, minPause time.Duration, progress func(done int)) (int, error) {
	var cur MeasureCursor
	total := 0
	for !cur.Done() {
		t0 := time.Now()
		next, n, err := s.MeasureDOMSizes(cur, batch)
		if err != nil {
			return total, err
		}
		cur, total = next, total+n
		if progress != nil {
			progress(total)
		}
		if !cur.Done() {
			time.Sleep(max(minPause, 3*time.Since(t0)))
		}
	}
	return total, nil
}
