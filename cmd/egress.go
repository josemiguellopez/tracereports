package main

import (
	"log/slog"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/egress"
)

// logEgress lists at startup every external destination the configuration sends data to (the same
// list `doctor --security` checks), or says there is none.
func logEgress(c ai.Config) {
	dests := egress.Destinations(c)
	if len(dests) == 0 {
		slog.Info("egress: no external destination configured (nothing leaves this server)")
		return
	}
	for _, d := range dests {
		slog.Info("egress", "kind", d.Kind, "name", d.Name, "host", d.Host, "scope", d.Scope, "sends", d.What)
	}
}
