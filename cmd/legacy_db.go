package main

import (
	"fmt"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// checkLegacyDB stops the start when DATA_DIR has '#', '?' or %XX and an older version kept the
// database somewhere else (it did not escape the path, see db.LegacyDatabase): starting would
// create an empty database and the data would look gone. Nothing is moved automatically.
// TRACEREPORTS_LEGACY_DB=ignore starts anyway with a new database at the right path.
func checkLegacyDB(path string) error {
	old := db.LegacyDatabase(path)
	if old == "" || strings.EqualFold(strings.TrimSpace(env.Get("LEGACY_DB")), "ignore") {
		return nil
	}
	return fmt.Errorf("the database is expected at %s, but an older version kept it at %s "+
		"(the folder has '#', '?' or '%%' and was not escaped). Stop TraceReports, move %s and its -wal and "+
		"-shm files (if any) to %s, and start again; or set TRACEREPORTS_LEGACY_DB=ignore to start with a new database",
		path, old, old, path)
}
