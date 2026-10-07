package ai

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestConsolePromptSection(t *testing.T) {
	if consolePromptSection(nil) != "" || consolePromptSection([]db.ConsoleEntry{{Level: "warning", Text: "x"}}) != "" {
		t.Fatal("no errors, no section")
	}
	got := consolePromptSection([]db.ConsoleEntry{
		{Level: "error", Text: "Failed   to load", Location: "main.js:1"},
		{Level: "log", Text: "noise"},
		{Level: "pageerror", Text: "TypeError: x is undefined"},
	})
	pe, er := strings.Index(got, "[pageerror] TypeError"), strings.Index(got, "[error] Failed to load (main.js:1)")
	if pe < 0 || er < 0 || pe > er || strings.Contains(got, "noise") {
		t.Fatalf("page errors first, logs left out:\n%s", got)
	}
	var many []db.ConsoleEntry
	for i := 0; i < 30; i++ {
		many = append(many, db.ConsoleEntry{Level: "error", Text: strings.Repeat("z", 500)})
	}
	if n := strings.Count(consolePromptSection(many), "\n- "); n != maxConsoleInPrompt {
		t.Fatalf("capped: %d", n)
	}
}
