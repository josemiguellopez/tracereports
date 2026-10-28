package builtin

import (
	"strconv"
	"strings"
)

// Incident is a group of failures of the run with the diagnosis of its first test.
type Incident struct {
	Title     string
	Tests     int
	Diagnosis Diagnosis
}

// Summarize writes the headline (empty: keep the default one) and the summary of a run from its
// incidents, biggest first. A mostly failed run points at the environment before any incident.
func Summarize(lang string, total, failed int, incidents []Incident) (headline, summary string) {
	if lang != "en" {
		lang = "es"
	}
	t := texts[lang]
	if len(incidents) == 0 {
		return "", ""
	}
	if total >= EnvDownMinTests && failed*100 >= EnvDownPercent*total {
		headline = fill(t["run_env_down"].Summary, map[string]string{
			"pct": strconv.Itoa(failed * 100 / total), "failed": strconv.Itoa(failed), "total": strconv.Itoa(total)})
	}
	counts := map[string]int{}
	for _, in := range incidents {
		cat := in.Diagnosis.Category
		if !in.Diagnosis.Conclusive {
			cat = "none"
		}
		counts[cat]++
	}
	var parts []string
	for _, cat := range []string{CategoryBackend, CategoryInfra, CategoryLocator, CategoryLogic, "none"} {
		if n := counts[cat]; n > 0 {
			parts = append(parts, fill(t["cat_"+cat].Summary, map[string]string{"n": strconv.Itoa(n)}))
		}
	}
	n := t["incident_1"].Summary
	if len(incidents) > 1 {
		n = fill(t["incident_n"].Summary, map[string]string{"n": strconv.Itoa(len(incidents))})
	}
	first := incidents[0].Diagnosis.Suggestion
	summary = fill(t["run_summary"].Summary, map[string]string{"incidents": n, "breakdown": strings.Join(parts, ", "), "first": first})
	return headline, summary
}

// DetectLang guesses the language of a test name: Spanish when it clearly is, otherwise English
// (the same rule the LLM follows when no language is configured).
func DetectLang(samples ...string) string {
	hits := 0
	for _, s := range samples {
		low := " " + strings.ToLower(s) + " "
		if strings.ContainsAny(low, "áéíóúñ¿¡") {
			return "es"
		}
		for _, w := range []string{" el ", " la ", " los ", " las ", " de ", " del ", " con ", " sin ", " que ", " debe ", " un ", " una ", " al ", " y ", " en ", " por ", " para "} {
			if strings.Contains(low, w) {
				hits++
			}
		}
	}
	if hits >= 2 {
		return "es"
	}
	return "en"
}
