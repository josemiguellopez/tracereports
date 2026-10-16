package db

import (
	"database/sql"
	"strings"
)

// The run explorer's text index (FTS5, trigram: substring search) and the tags of each run.
//
// Each row of run_search_text is keyed by its rowid, which SQLite finds directly: a run is
// -run.id, a test is test.id (its name, key and error message) and a failed network call is
// networkRowBase + network.id (its method and URL). Updates and deletes (retention, a run deleted
// from the UI) touch only their own rows instead of scanning the index, and run_id (UNINDEXED) is
// only read back for the matches.
//
// runSearchIndexVersion is what the index holds; a database with an older one is rebuilt once:
// 1 = runs and test names, 2 = also error messages and failed calls.
const runSearchIndexSchema = `
DROP TRIGGER IF EXISTS run_search_runs_ai;
DROP TRIGGER IF EXISTS run_search_runs_au;
DROP TRIGGER IF EXISTS run_search_runs_ad;
DROP TRIGGER IF EXISTS run_search_tests_ai;
DROP TRIGGER IF EXISTS run_search_tests_au;
DROP TRIGGER IF EXISTS run_search_tests_ad;
DROP TABLE IF EXISTS run_search_fts;
CREATE VIRTUAL TABLE IF NOT EXISTS run_search_text USING fts5(run_id UNINDEXED, content, tokenize='trigram');
CREATE TABLE IF NOT EXISTS run_search_init (done INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS run_tags (
	run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	tag TEXT NOT NULL COLLATE NOCASE,
	PRIMARY KEY(run_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_run_tags_tag ON run_tags(tag, run_id);
CREATE TRIGGER IF NOT EXISTS run_search_text_runs_ai AFTER INSERT ON runs BEGIN
	INSERT INTO run_search_text(rowid, run_id, content)
	VALUES(-new.id, new.id, new.name || ' ' || new.project || ' ' || new.branch || ' ' || new.commit_sha || ' ' || new.environment);
END;
CREATE TRIGGER IF NOT EXISTS run_search_text_runs_au AFTER UPDATE OF name, project, branch, commit_sha, environment ON runs BEGIN
	DELETE FROM run_search_text WHERE rowid = -old.id;
	INSERT INTO run_search_text(rowid, run_id, content)
	VALUES(-new.id, new.id, new.name || ' ' || new.project || ' ' || new.branch || ' ' || new.commit_sha || ' ' || new.environment);
END;
CREATE TRIGGER IF NOT EXISTS run_search_text_runs_ad AFTER DELETE ON runs BEGIN
	DELETE FROM run_search_text WHERE rowid = -old.id;
END;
DROP TRIGGER IF EXISTS run_search_text_tests_au;
CREATE TRIGGER IF NOT EXISTS run_search_text_tests_ai AFTER INSERT ON tests BEGIN
	INSERT INTO run_search_text(rowid, run_id, content) VALUES(new.id, new.run_id, new.name || ' ' || new.test_key || ' ' || new.error_message);
END;
CREATE TRIGGER IF NOT EXISTS run_search_text_tests_au2 AFTER UPDATE OF name, test_key, error_message ON tests BEGIN
	DELETE FROM run_search_text WHERE rowid = old.id;
	INSERT INTO run_search_text(rowid, run_id, content) VALUES(new.id, new.run_id, new.name || ' ' || new.test_key || ' ' || new.error_message);
END;
CREATE TRIGGER IF NOT EXISTS run_search_text_tests_ad AFTER DELETE ON tests BEGIN
	DELETE FROM run_search_text WHERE rowid = old.id;
END;
CREATE TRIGGER IF NOT EXISTS run_search_text_net_ai AFTER INSERT ON network WHEN new.failed = 1 OR new.status >= 400 BEGIN
	INSERT INTO run_search_text(rowid, run_id, content)
	VALUES(1099511627776 + new.id, (SELECT run_id FROM tests WHERE id = new.test_id), new.method || ' ' || new.url);
END;
CREATE TRIGGER IF NOT EXISTS run_search_text_net_ad AFTER DELETE ON network WHEN old.failed = 1 OR old.status >= 400 BEGIN
	DELETE FROM run_search_text WHERE rowid = 1099511627776 + old.id;
END;
`

// networkRowBase separates the failed calls from the tests in the index's rowids (2^40).
const networkRowBase = 1 << 40

const runSearchIndexVersion = 2

// ensureRunSearchIndex creates the indexes of the run explorer and, only the first time (an
// existing database), fills them from the runs and tests already stored. The fill and its mark
// are one transaction: an interruption leaves nothing and the next start repeats it. Afterwards
// the triggers and CreateTestWithMeta keep them current.
func ensureRunSearchIndex(sqldb *sql.DB) error {
	if _, err := sqldb.Exec(runSearchIndexSchema); err != nil {
		return err
	}
	var version int
	if err := sqldb.QueryRow(`SELECT COALESCE(MAX(done), 0) FROM run_search_init`).Scan(&version); err != nil || version >= runSearchIndexVersion {
		return err
	}
	tx, err := sqldb.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM run_search_text`,
		`INSERT INTO run_search_text(rowid, run_id, content)
			SELECT -id, id, name || ' ' || project || ' ' || branch || ' ' || commit_sha || ' ' || environment FROM runs`,
		`INSERT INTO run_search_text(rowid, run_id, content) SELECT id, run_id, name || ' ' || test_key || ' ' || error_message FROM tests`,
		`INSERT INTO run_search_text(rowid, run_id, content)
			SELECT 1099511627776 + n.id, t.run_id, n.method || ' ' || n.url FROM network n JOIN tests t ON t.id = n.test_id
			WHERE n.failed = 1 OR n.status >= 400`,
		`DELETE FROM run_search_init`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT run_id, category FROM tests WHERE category <> ''`)
	if err != nil {
		return err
	}
	type tagged struct {
		run      int64
		category string
	}
	var all []tagged
	for rows.Next() {
		var x tagged
		if err := rows.Scan(&x.run, &x.category); err != nil {
			rows.Close()
			return err
		}
		all = append(all, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range all {
		if err := addRunTags(tx, x.run, x.category); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO run_search_init(done) VALUES(?)`, runSearchIndexVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func splitTags(category string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, tag := range strings.Split(category, ",") {
		if tag = strings.TrimSpace(tag); tag != "" && !seen[strings.ToLower(tag)] {
			seen[strings.ToLower(tag)] = true
			out = append(out, tag)
		}
	}
	return out
}

func addRunTags(q querier, runID int64, category string) error {
	for _, tag := range splitTags(category) {
		if _, err := q.Exec(`INSERT OR IGNORE INTO run_tags(run_id,tag) VALUES(?,?)`, runID, tag); err != nil {
			return err
		}
	}
	return nil
}
