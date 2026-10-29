package tracereports

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMaskSelector(t *testing.T) {
	t.Setenv("TRACEREPORTS_SCREENSHOT_MASK", "")
	t.Setenv("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "")
	if got := MaskSelector(); got != "input[type=password], [data-tracereports-mask]" {
		t.Fatalf("defaults: %q", got)
	}
	t.Setenv("TRACEREPORTS_SCREENSHOT_MASK", " input[name=rut], .tarjeta, ")
	if got := MaskSelector(); got != "input[type=password], [data-tracereports-mask], input[name=rut], .tarjeta" {
		t.Fatalf("extra: %q", got)
	}
	t.Setenv("TRACEREPORTS_SCREENSHOT_MASK_DEFAULTS", "OFF")
	if got := MaskSelector(); got != "input[name=rut], .tarjeta" {
		t.Fatalf("sin los de siempre: %q", got)
	}
	t.Setenv("TRACEREPORTS_SCREENSHOT_MASK", "")
	if got := MaskSelector(); got != "" {
		t.Fatalf("nada: %q", got)
	}
}

func TestScreenshotAllowed(t *testing.T) {
	for _, c := range []struct {
		policy, status string
		want           bool
	}{
		{"", Info, true}, {"all", Info, true}, {"basura", Info, true},
		{"failures", Info, false}, {"failures", Fail, true}, {"failures", "fail", true},
		{"off", Info, false}, {"off", Fail, false},
	} {
		t.Setenv("TRACEREPORTS_SCREENSHOTS", c.policy)
		if got := ScreenshotAllowed(c.status); got != c.want {
			t.Errorf("%q/%q = %v", c.policy, c.status, got)
		}
	}
}

// Test.Screenshot no envía lo que TRACEREPORTS_SCREENSHOTS no permite.
func TestScreenshotFollowsThePolicy(t *testing.T) {
	for _, c := range []struct {
		policy string
		sent   int32
	}{{"all", 2}, {"failures", 1}, {"off", 0}} {
		t.Run(c.policy, func(t *testing.T) {
			t.Setenv("TRACEREPORTS_SCREENSHOTS", c.policy)
			var shots int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/runs":
					w.Write([]byte(`{"run_id":7}`))
				case strings.HasSuffix(r.URL.Path, "/tests"):
					w.Write([]byte(`{"test_id":3}`))
				case strings.HasSuffix(r.URL.Path, "/screenshot"):
					atomic.AddInt32(&shots, 1)
					w.Write([]byte(`{"url":"/screenshots/a.png"}`))
				default:
					w.Write([]byte(`{}`))
				}
			}))
			defer srv.Close()
			cl := New(srv.URL)
			cl.Offline = "off"
			cl.StartRun("capturas", "")
			tc, err := cl.StartTest("t", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if url, err := tc.Screenshot([]byte("PNG"), "paso", Info); err != nil || (c.policy == "all") != (url != "") {
				t.Fatalf("paso: %q %v", url, err)
			}
			tc.Screenshot([]byte("PNG"), "fallo", Fail)
			tc.Finish(Fail, "boom", "")
			cl.FinishRun()
			if got := atomic.LoadInt32(&shots); got != c.sent {
				t.Fatalf("%d capturas enviadas, quería %d", got, c.sent)
			}
		})
	}
}

// Los scripts para Selenium/chromedp reciben selector, color y atributo, y se deshacen por el atributo.
func TestCoverScriptsUseTheirArguments(t *testing.T) {
	for _, want := range []string{"const [sel, color, attr] = arguments", "querySelectorAll(sel)", "setAttribute(attr", "background:\" + color"} {
		if !strings.Contains(CoverScript, want) {
			t.Errorf("CoverScript sin %q", want)
		}
	}
	if !strings.Contains(UncoverScript, `"[" + arguments[0] + "]"`) || !strings.Contains(UncoverScript, ".remove()") {
		t.Errorf("UncoverScript: %s", UncoverScript)
	}
}
