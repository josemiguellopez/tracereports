package allure

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture(t *testing.T) *Report {
	t.Helper()
	rep, err := Parse(os.DirFS(filepath.Join("testdata", "allure-results")))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func byName(rep *Report) map[string]Result {
	out := map[string]Result{}
	for _, r := range rep.Results {
		out[r.Name] = r
	}
	return out
}

func TestParseFolder(t *testing.T) {
	rep := fixture(t)
	if rep.Build != "Nightly #42" {
		t.Fatalf("build: %q", rep.Build)
	}
	if len(rep.Results) != 4 { // test_coupon tiene dos ejecuciones: se une en una
		t.Fatalf("results: %d", len(rep.Results))
	}
	if rep.Results[0].Name != "test_pay_with_card" || rep.Results[3].Name != "test_coupon" {
		t.Fatalf("ordered by start: %s ... %s", rep.Results[0].Name, rep.Results[3].Name)
	}
	r := byName(rep)

	pay := r["test_pay_with_card"]
	if pay.Status != Fail || pay.Message != "AssertionError: expected 200, got 500" || !strings.Contains(pay.Trace, "test_checkout.py:42") {
		t.Fatalf("failure: %+v", pay)
	}
	if pay.Key() != "tests.test_checkout.TestPay#test_pay_with_card [card=visa]" || pay.Params != "card=visa" {
		t.Fatalf("identity: %q %q", pay.Key(), pay.Params)
	}
	if pay.Suite != "tests / test_checkout / TestPay" || pay.Framework != "pytest" || pay.Description == "" {
		t.Fatalf("labels: %+v", pay)
	}
	// tags, feature y story, sin repetir (SMOKE = smoke)
	if strings.Join(pay.Tags, ",") != "smoke,Checkout,Pago" {
		t.Fatalf("tags: %v", pay.Tags)
	}
	if pay.Start != 1790000000000 || pay.Stop != 1790000004000 || pay.Attempts != 1 {
		t.Fatalf("times: %+v", pay)
	}
	if len(pay.Steps) != 2 || len(pay.Steps[0].Steps) != 1 || pay.Steps[0].Steps[0].Name != "Login" {
		t.Fatalf("nested steps: %+v", pay.Steps)
	}
	failed := pay.Steps[1]
	if failed.Status != Fail || failed.Message != "500 en /api/pay" || len(failed.Attachments) != 1 || !failed.Attachments[0].IsImage() {
		t.Fatalf("failed step: %+v", failed)
	}
	img, err := failed.Attachments[0].Read()
	if err != nil || !bytes.HasPrefix(img, []byte("\x89PNG")) {
		t.Fatalf("image attachment: %v", err)
	}
	if len(pay.Attachments) != 2 || pay.Attachments[0].IsImage() {
		t.Fatalf("test attachments: %+v", pay.Attachments)
	}
	if _, err := pay.Attachments[1].Read(); err == nil {
		t.Fatal("a missing attachment file is an error, not a crash")
	}

	if r["test_db"].Status != Fail { // broken: un error, también es fallo
		t.Fatalf("broken: %+v", r["test_db"])
	}
	if r["test_refund"].Status != Skip || r["test_refund"].Message != "sin datos" {
		t.Fatalf("skipped: %+v", r["test_refund"])
	}
	coupon := r["test_coupon"]
	if coupon.Status != Pass || coupon.Attempts != 2 || coupon.Start != 1790000006000 {
		t.Fatalf("retry keeps the last execution and counts attempts: %+v", coupon)
	}
}

func TestParseZipWithTheFolderInside(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	root := filepath.Join("testdata", "allure-results")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(root, e.Name()))
		w, _ := zw.Create("allure-results/" + e.Name())
		w.Write(data)
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Parse(zr)
	if err != nil || len(rep.Results) != 4 || rep.Build != "Nightly #42" {
		t.Fatalf("zip: %v %+v", err, rep)
	}
	img, err := byName(rep)["test_pay_with_card"].Steps[1].Attachments[0].Read()
	if err != nil || len(img) == 0 {
		t.Fatalf("attachment inside the zip: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"empty":        fstest.MapFS{},
		"not allure":   fstest.MapFS{"report.xml": {Data: []byte("<testsuite/>")}},
		"invalid json": fstest.MapFS{"x-result.json": {Data: []byte("{")}},
		"two folders": fstest.MapFS{
			"a/x-result.json": {Data: []byte(`{"name":"a"}`)},
			"b/y-result.json": {Data: []byte(`{"name":"b"}`)},
		},
	} {
		if _, err := Parse(fsys); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestEdgeValues(t *testing.T) {
	rep, err := Parse(fstest.MapFS{
		"a-result.json": {Data: []byte(`{"fullName":"pkg.C#only_full","status":"unknown"}`)},
		"b-result.json": {Data: []byte(`{"status":"passed"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := byName(rep)
	if r["pkg.C#only_full"].Status != Skip { // unknown: no terminó su reporte, no es un PASS
		t.Fatalf("unknown status: %+v", r)
	}
	if _, ok := r["(unnamed test)"]; !ok {
		t.Fatalf("unnamed: %+v", r)
	}
	if rep.Build != "" {
		t.Fatalf("no executor.json: %q", rep.Build)
	}
}

func TestAttachmentSourceCannotLeaveTheFolder(t *testing.T) {
	a := Attachment{Source: "../secret.png", fsys: fstest.MapFS{}}
	if _, err := a.Read(); err == nil {
		t.Fatal("a source with a path must be rejected")
	}
	big := Attachment{Source: "big.png", fsys: fstest.MapFS{"big.png": {Data: make([]byte, maxAttachment+1)}}}
	if _, err := big.Read(); err == nil {
		t.Fatal("attachments larger than the limit must be rejected")
	}
}
