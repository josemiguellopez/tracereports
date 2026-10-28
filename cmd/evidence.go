package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/db"
)

const evidenceUsage = `Usage: tracereports evidence status|encrypt|rekey [-data-dir DIR] [-dry-run]

  status   says how the screenshots, traces, videos and DOM snapshots are stored: in clear, or
           encrypted and with which key (its id, never the key or the content)
  encrypt  encrypts with TRACEREPORTS_DB_KEY the evidence stored in clear (kept before
           TRACEREPORTS_ENCRYPT_AT_REST=1) and re-encrypts what TRACEREPORTS_DB_KEY_PREVIOUS encrypted
  rekey    the same, after a key rotation: new key in TRACEREPORTS_DB_KEY, old one in
           TRACEREPORTS_DB_KEY_PREVIOUS; when it ends, the previous key is no longer needed

Stop the server and back up the data folder first. Keep the key outside DATA_DIR: without it the
encrypted evidence cannot be read. Files are rewritten next to the old ones and renamed, but the
old plaintext may survive in free disk blocks: on a disk that held sensitive evidence, rely on an
encrypted volume as well.
`

// runEvidence is "tracereports evidence ...": it works on DATA_DIR (or -data-dir).
func runEvidence(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, evidenceUsage)
		return errors.New("missing command: status, encrypt or rekey")
	}
	cmd := args[0]
	if cmd == "-h" || cmd == "-help" || cmd == "--help" || cmd == "help" {
		fmt.Fprint(out, evidenceUsage)
		return nil
	}
	fl := flag.NewFlagSet("evidence "+cmd, flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(fl.Output(), evidenceUsage); fl.PrintDefaults() }
	if _, err := loadDotEnv(envFileName()); err != nil {
		return err
	}
	dataDir := fl.String("data-dir", envOr("DATA_DIR", "./data"), "data folder with tracereports.db and screenshots/")
	dry := fl.Bool("dry-run", false, "only say what would change")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	if cmd != "status" && cmd != "encrypt" && cmd != "rekey" {
		fmt.Fprint(out, evidenceUsage)
		return fmt.Errorf("unknown command %q", cmd)
	}
	vault, err := atrest.FromEnv(*dataDir)
	if err != nil {
		return err
	}
	write := cmd != "status" && !*dry
	if cmd != "status" {
		if vault == nil {
			return errors.New("TRACEREPORTS_DB_KEY is not set: there is no key to encrypt with")
		}
		vault.EncryptNew = true // el comando cifra aunque el servidor todavía no lo haga
	}
	store, err := db.OpenExisting(filepath.Join(*dataDir, "tracereports.db"), !write)
	if err != nil {
		return err
	}
	defer store.Close()
	return evidenceOn(store, atrest.Dir{Path: filepath.Join(*dataDir, "screenshots"), Vault: vault}, cmd, *dry, out)
}

func evidenceOn(store *db.Store, shots atrest.Dir, cmd string, dry bool, out io.Writer) error {
	current := shots.Vault.KeyID()
	describe := func(id string) string {
		switch {
		case id == "":
			return "in clear"
		case id == current:
			return "encrypted (current key " + id + ")"
		case shots.Vault != nil && shots.Vault.CanOpen(id):
			return "encrypted (previous key " + id + ")"
		default:
			return "encrypted with an unknown key " + id
		}
	}
	report := func(what string, keys map[string]int) {
		ids := make([]string, 0, len(keys))
		for id := range keys {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			fmt.Fprintf(out, "%s: none\n", what)
		}
		for _, id := range ids {
			fmt.Fprintf(out, "%s: %d %s\n", what, keys[id], describe(id))
		}
	}

	names, err := evidenceFiles(shots.Path)
	if err != nil {
		return err
	}
	fileKeys := map[string]int{}
	for _, name := range names {
		id, err := shots.KeyOf(name)
		if err != nil {
			return err
		}
		fileKeys[id]++
	}
	hasDOM, err := store.HasTable("test_dom")
	if err != nil {
		return err
	}
	domKeys := map[string]int{}
	if hasDOM {
		if domKeys, err = store.DOMKeys(); err != nil {
			return err
		}
	}
	if cmd == "status" || dry {
		report("files", fileKeys)
		report("DOM snapshots", domKeys)
		if dry {
			pending := func(keys map[string]int) (n int) {
				for id, c := range keys {
					if id != current {
						n += c
					}
				}
				return n
			}
			fmt.Fprintf(out, "dry run: %d files and %d DOM snapshots would be (re)encrypted with key %s\n", pending(fileKeys), pending(domKeys), current)
		}
		return nil
	}

	changed, unreadable := 0, 0
	for _, name := range names {
		ok, err := shots.Reseal(name)
		switch {
		case errors.Is(err, atrest.ErrNoKey):
			unreadable++
		case err != nil:
			return fmt.Errorf("%s: %w", name, err)
		case ok:
			changed++
		}
	}
	domChanged, domUnreadable := 0, 0
	if hasDOM {
		if domChanged, domUnreadable, err = store.ResealDOM(shots.Vault); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "encrypted with key %s: %d files, %d DOM snapshots\n", current, changed, domChanged)
	if unreadable+domUnreadable > 0 {
		return fmt.Errorf("%d files and %d DOM snapshots are encrypted with a key that is not configured: set it in TRACEREPORTS_DB_KEY_PREVIOUS and run again", unreadable, domUnreadable)
	}
	return nil
}

// evidenceFiles lists the files of the screenshots folder (a missing folder has none).
func evidenceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		// los temporales de una reescritura cortada no son evidencia
		if e.Type().IsRegular() && !strings.HasSuffix(e.Name(), ".reseal") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
