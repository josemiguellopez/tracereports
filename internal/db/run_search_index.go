package db

import (
	"database/sql"
	"strings"
)

const runSearchIndexSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS run_search_fts USING fts5(
	run_id UNINDEXED, test_id UNINDEXED, kind UNINDEXED, content, tokenize='trigram'
);
CREATE TABLE IF NOT EXISTS run_tags (
	run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	tag TEXT NOT NULL COLLATE NOCASE,
	PRIMARY KEY(run_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_run_tags_tag ON run_tags(tag, run_id);
CREATE TRIGGER IF NOT EXISTS run_search_runs_ai AFTER INSERT ON runs BEGIN
	INSERT INTO run_search_fts(run_id,test_id,kind,content) VALUES(new.id,0,'run',new.name || ' ' || new.project || ' ' || new.branch || ' ' || new.commit_sha || ' ' || new.environment);
END;
CREATE TRIGGER IF NOT EXISTS run_search_runs_au AFTER UPDATE OF name,project,branch,commit_sha,environment ON runs BEGIN
	DELETE FROM run_search_fts WHERE run_id=old.id AND kind='run';
	INSERT INTO run_search_fts(run_id,test_id,kind,content) VALUES(new.id,0,'run',new.name || ' ' || new.project || ' ' || new.branch || ' ' || new.commit_sha || ' ' || new.environment);
END;
CREATE TRIGGER IF NOT EXISTS run_search_runs_ad AFTER DELETE ON runs BEGIN
	DELETE FROM run_search_fts WHERE run_id=old.id;
END;
CREATE TRIGGER IF NOT EXISTS run_search_tests_ai AFTER INSERT ON tests BEGIN
	INSERT INTO run_search_fts(run_id,test_id,kind,content) VALUES(new.run_id,new.id,'test',new.name || ' ' || new.test_key);
END;
CREATE TRIGGER IF NOT EXISTS run_search_tests_au AFTER UPDATE OF name,test_key ON tests BEGIN
	DELETE FROM run_search_fts WHERE test_id=old.id AND kind='test';
	INSERT INTO run_search_fts(run_id,test_id,kind,content) VALUES(new.run_id,new.id,'test',new.name || ' ' || new.test_key);
END;
CREATE TRIGGER IF NOT EXISTS run_search_tests_ad AFTER DELETE ON tests BEGIN
	DELETE FROM run_search_fts WHERE test_id=old.id AND kind='test';
END;
`

// ensureRunSearchIndex creates and backfills the two indexes used by the run explorer. Triggers
// keep the text index current while the tags table is populated by CreateTestWithMeta.
func ensureRunSearchIndex(sqldb *sql.DB) error {
	if _, err := sqldb.Exec(runSearchIndexSchema); err != nil {
		return err
	}
	if _, err := sqldb.Exec(`INSERT INTO run_search_fts(run_id,test_id,kind,content)
		SELECT r.id,0,'run',r.name || ' ' || r.project || ' ' || r.branch || ' ' || r.commit_sha || ' ' || r.environment FROM runs r
		WHERE NOT EXISTS (SELECT 1 FROM run_search_fts f WHERE f.run_id=r.id AND f.kind='run')`); err != nil {
		return err
	}
	if _, err := sqldb.Exec(`INSERT INTO run_search_fts(run_id,test_id,kind,content)
		SELECT t.run_id,t.id,'test',t.name || ' ' || t.test_key FROM tests t
		WHERE NOT EXISTS (SELECT 1 FROM run_search_fts f WHERE f.test_id=t.id AND f.kind='test')`); err != nil {
		return err
	}
	rows, err := sqldb.Query(`SELECT id, category FROM tests WHERE category<>''`)
	if err != nil {
		return err
	}
	type taggedTest struct {
		id       int64
		category string
	}
	var tests []taggedTest
	for rows.Next() {
		var x taggedTest
		if err := rows.Scan(&x.id, &x.category); err != nil {
			rows.Close()
			return err
		}
		tests = append(tests, x)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	stmt, err := sqldb.Prepare(`INSERT OR IGNORE INTO run_tags(run_id,tag) SELECT run_id,? FROM tests WHERE id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, test := range tests {
		for _, tag := range splitTags(test.category) {
			if _, err := stmt.Exec(tag, test.id); err != nil {
				return err
			}
		}
	}
	return nil
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
