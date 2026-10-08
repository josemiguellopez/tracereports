package main

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// settleAIBudget reports, on start, an AI budget ledger left unfinished by an earlier
// (non-atomic) version of its migration, and applies the decision given in
// TRACEREPORTS_AI_BUDGET_RECOVERY: "backfill" counts the diagnoses missing from the ledger as
// already spent (they may include manual re-analyses), "keep" leaves the ledger as it is. The
// data cannot tell which is right, so nothing is decided without it (see db.ensureAIBudget).
func settleAIBudget(store *db.Store) error {
	pending, missing, err := store.AIBudgetMigration()
	if err != nil || !pending {
		return err
	}
	switch choice := strings.ToLower(strings.TrimSpace(env.Get("AI_BUDGET_RECOVERY"))); choice {
	case "backfill", "keep":
		added, err := store.ResolveAIBudgetMigration(choice == "backfill")
		if err != nil {
			return fmt.Errorf("ai budget recovery: %w", err)
		}
		slog.Info("AI budget ledger settled", "choice", choice, "diagnoses_counted", added)
	default:
		slog.Warn("the AI budget ledger may be missing analyses spent before the upgrade (an interrupted migration "+
			"of a development build): it cannot tell them from manual re-analyses done later. Until you decide, they do not "+
			"count against TRACEREPORTS_AI_MAX_PER_RUN. Start once with TRACEREPORTS_AI_BUDGET_RECOVERY=backfill to count "+
			"them, or =keep to leave the ledger as it is", "diagnoses_not_in_ledger", missing)
	}
	return nil
}
