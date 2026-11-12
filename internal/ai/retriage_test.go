package ai

import (
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
)

// Los diagnósticos guardados antes de las señales de carga (LOCATOR_CHANGED en el test 5148 después de
// actualizar el servidor) se revisan una vez por versión de reglas, sin «Reanalizar» y sin IA.
func TestSavedDiagnosesAreRevisedOnceWithTheNewRules(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	loading := clientCase(t, store, runID)
	saveDOM(t, store, loading, skeletonPage(false))
	other := failAt(t, store, runID, "test_003_otro", "Captura al fallar", "", 10_000)
	store.FinishTest(other, "FAIL", `NoSuchElementException: no such element: {"selector":"#guardar"}`, "")
	for _, id := range []int64{loading, other} {
		store.SetTriagePending(id)
		store.SaveTriage(id, "LOCATOR_CHANGED", "Probablemente el selector del elemento ha cambiado", "Actualizar el selector", "", "")
	}
	store.FinishRun(runID)
	a := New(store)
	a.RetriageOnUpgrade()
	got, _ := store.GetTest(loading)
	if got.Triage.Category != builtin.CategoryTiming || got.Triage.Provider != ProviderBuiltin || got.Triage.Rule != "still_loading" || strings.Contains(got.Triage.Summary, "ha cambiado") {
		t.Fatalf("revised: %+v", got.Triage)
	}
	if o, _ := store.GetTest(other); o.Triage.Category != "LOCATOR_CHANGED" {
		t.Fatalf("without loading signals it stays: %+v", o.Triage)
	}
	if s, _ := store.Settings(); s[settingRetriaged] != builtin.Version {
		t.Fatalf("done once per rules version: %q", s[settingRetriaged])
	}
	// una segunda vez con la misma versión no hace nada (ni siquiera mira)
	store.SaveTriage(loading, "LOCATOR_CHANGED", "otra vez", "", "", "")
	a.RetriageOnUpgrade()
	if g, _ := store.GetTest(loading); g.Triage.Category != "LOCATOR_CHANGED" {
		t.Fatalf("once per version: %+v", g.Triage)
	}
	if n, err := a.RetriageLoading(time.Hour); err != nil || n != 1 {
		t.Fatalf("on demand: %d %v", n, err)
	}
}
