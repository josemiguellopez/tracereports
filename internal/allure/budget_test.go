package allure

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// countingFS cuenta los bytes que realmente se leen de cada archivo.
type countingFS struct {
	fstest.MapFS
	read map[string]int
}

type countingFile struct {
	fs.File
	name string
	fsys *countingFS
}

func (c *countingFS) Open(name string) (fs.File, error) {
	f, err := c.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: f, name: name, fsys: c}, nil
}

func (f *countingFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.fsys.read[f.name] += n
	return n, err
}

const okResult = `{"uuid":"1","name":"t1","status":"passed","attachments":[{"name":"log","source":"a-attachment.txt","type":"text/plain"}]}`

func TestExecutorJSONIsNotReadPastItsLimit(t *testing.T) {
	c := &countingFS{MapFS: fstest.MapFS{
		"1-result.json": {Data: []byte(okResult)},
		"executor.json": {Data: []byte(`{"buildName":"` + strings.Repeat("x", 5<<20) + `"}`)},
	}, read: map[string]int{}}
	rep, err := Parse(c)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Build != "" {
		t.Fatal("an oversized executor.json is ignored")
	}
	if got := c.read["executor.json"]; got > maxExecutor+64<<10 {
		t.Fatalf("read %d bytes of a 5 MiB executor.json: it must stop at the limit", got)
	}
}

func TestBudgetCapsJSONAndTotal(t *testing.T) {
	files := fstest.MapFS{
		"1-result.json":    {Data: []byte(okResult)},
		"2-result.json":    {Data: []byte(okResult)},
		"a-attachment.txt": {Data: []byte(strings.Repeat("y", 4000))},
	}
	// JSON: dos result.json no caben en el presupuesto de uno
	if _, err := ParseBudget(files, &Budget{Total: 1 << 20, JSON: int64(len(okResult)) + 10}); !errors.Is(err, ErrBudget) {
		t.Fatalf("JSON budget: %v", err)
	}
	// total: el JSON cabe, el adjunto ya no (se cobra al leerlo)
	b := &Budget{Total: int64(2*len(okResult)) + 1000, JSON: 1 << 20}
	rep, err := ParseBudget(files, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rep.Results[0].Attachments[0].Read(); !errors.Is(err, ErrBudget) {
		t.Fatalf("attachment past the total budget: %v", err)
	}
	// con presupuesto suficiente todo se lee igual que sin él
	b = &Budget{Total: 1 << 20, JSON: 1 << 20}
	rep, err = ParseBudget(files, b)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := rep.Results[0].Attachments[0].Read(); err != nil || len(data) != 4000 {
		t.Fatalf("read: %d %v", len(data), err)
	}
	if want := int64(1<<20) - int64(2*len(okResult)) - 4000; b.Total != want {
		t.Fatalf("budget charged with the bytes read: %d, want %d", b.Total, want)
	}
}
