package ai

import (
	"log/slog"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// settingRetriaged: the version of the local rules the saved diagnoses were last revised with.
const settingRetriaged = "ai_retriaged_rules"

// retriageWindow: how far back a saved diagnosis is revised when the rules change.
const retriageWindow = 30 * 24 * time.Hour

// RetriageLoading revises the per-test diagnoses saved as LOCATOR_CHANGED (by the local engine or by an
// AI) before the loading signals existed: when the failure page or its network says it was still
// loading, the diagnosis becomes the local engine's TIMING, without calling any AI. A test already
// re-analyzed by hand is revised the same way (the rule wins over "the selector changed" there too).
// It returns how many changed.
func (a *Analyzer) RetriageLoading(since time.Duration) (int, error) {
	ids, err := a.store.TriagedSince(builtin.CategoryLocator, db.NowMs()-since.Milliseconds())
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, id := range ids {
		t, err := a.store.GetTest(id)
		if err != nil {
			continue // la borró la retención mientras tanto
		}
		in, err := a.builtinInput(t, a.builtinLang(t.Name, t.Description))
		if err != nil || (len(in.Loading) == 0 && in.SelectorBefore == "") {
			continue
		}
		d := a.redactDiagnosis(builtin.Diagnose(in))
		if d.Category != builtin.CategoryTiming {
			continue
		}
		ok, err := a.store.ReclassifyTriage(id, builtin.CategoryLocator, d.Category, d.Summary, d.Suggestion, ProviderBuiltin, d.Rule)
		if err != nil {
			return changed, err
		}
		if ok {
			changed++
			a.changed("test", t.RunID, id)
		}
	}
	return changed, nil
}

// RetriageOnUpgrade runs RetriageLoading once per version of the local rules (in the background at
// startup): the saved diagnoses of the last 30 days get what the new rules see, without "Reanalyze".
func (a *Analyzer) RetriageOnUpgrade() {
	saved, err := a.store.Settings()
	if err != nil || saved[settingRetriaged] == builtin.Version {
		return
	}
	n, err := a.RetriageLoading(retriageWindow)
	if err != nil {
		slog.Warn("ai: revise the saved diagnoses with the new rules", "err", err, "changed", n)
		return
	}
	if err := a.store.SaveSettings(map[string]string{settingRetriaged: builtin.Version}); err != nil {
		slog.Warn("ai: save the rules version of the revised diagnoses", "err", err)
	}
	slog.Info("ai: saved diagnoses revised with the new rules", "rules", builtin.Version, "changed", n)
}
