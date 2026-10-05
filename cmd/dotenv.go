package main

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// loadDotEnv reads KEY=VALUE lines from path into the environment, so the binary honors the same
// .env that Docker Compose uses. Variables already set in the environment win. Blank lines and
// "#" comments are ignored; "export " prefixes and surrounding quotes are accepted. A missing file
// is not an error. Returns how many variables were applied.
func loadDotEnv(path string) (int, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i]) // comentario al final de la línea
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
}
