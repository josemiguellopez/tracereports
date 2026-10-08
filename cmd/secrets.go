package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
	"github.com/josemiguellopez/tracereports/internal/secret"
)

const secretsUsage = `Usage: tracereports secrets status|migrate [-data-dir DIR] [-dry-run]

  status   says how each credential saved from Settings is stored (never its value)
  migrate  encrypts with TRACEREPORTS_SECRET_KEY the credentials saved in clear by older versions,
           and re-encrypts with the new key the ones encrypted with TRACEREPORTS_SECRET_KEY_PREVIOUS

Back up the data folder before migrating, and keep TRACEREPORTS_SECRET_KEY: without it the
encrypted credentials cannot be read (they are not lost; set the key again or type them again).
`

// runSecrets is "tracereports secrets ...": it works on the database of DATA_DIR (or -data-dir).
func runSecrets(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, secretsUsage)
		return errors.New("missing command: status or migrate")
	}
	cmd := args[0]
	fl := flag.NewFlagSet("secrets "+cmd, flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(fl.Output(), secretsUsage); fl.PrintDefaults() }
	if _, err := loadDotEnv(envFileName()); err != nil {
		return err
	}
	dataDir := fl.String("data-dir", envOr("DATA_DIR", "./data"), "data folder with tracereports.db")
	dry := fl.Bool("dry-run", false, "only say what would change")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	if cmd != "status" && cmd != "migrate" {
		fmt.Fprint(out, secretsUsage)
		return fmt.Errorf("unknown command %q", cmd)
	}
	dbPath := filepath.Join(*dataDir, "tracereports.db")
	box, err := secret.FromEnv()
	if err != nil {
		return err
	}
	if cmd == "migrate" && !box.Enabled() {
		return errors.New("TRACEREPORTS_SECRET_KEY is not set: there is no key to encrypt with")
	}
	// sin esquema ni migraciones (no es el servidor) y sin crear la base: status y -dry-run la
	// abren de solo lectura; migrate solo cambia las filas de settings que cifra
	store, err := db.OpenExisting(dbPath, cmd == "status" || *dry)
	if err != nil {
		return err
	}
	defer store.Close()
	return secretsOn(store, box, cmd, *dry, out)
}

func secretsOn(store *db.Store, box *secret.Box, cmd string, dry bool, out io.Writer) error {
	if ok, err := store.HasTable("settings"); err != nil {
		return err
	} else if !ok {
		// base de una versión anterior a la pantalla de Ajustes: no puede tener credenciales guardadas
		fmt.Fprintln(out, "this database has no settings table (created by an older version): no saved credentials, nothing to migrate")
		return nil
	}
	saved, err := store.Settings()
	if err != nil {
		return err
	}
	if box.Enabled() {
		fmt.Fprintf(out, "master key: set (id %s)\n", box.KeyID())
	} else {
		fmt.Fprintln(out, "master key: not set (TRACEREPORTS_SECRET_KEY)")
	}
	changed, failed := 0, 0
	for _, name := range api.SecretSettings {
		stored := saved[name]
		if stored == "" {
			fmt.Fprintf(out, "%s: not saved\n", name)
			continue
		}
		plain, openErr := box.Open(name, stored)
		state := "encrypted"
		switch {
		case openErr != nil:
			state = "cannot be decrypted: " + openErr.Error()
		case !secret.IsSealed(stored):
			state = "in clear"
		case box.NeedsReseal(stored):
			state = "encrypted with the previous key (" + secret.KeyIDOf(stored) + ")"
		}
		fmt.Fprintf(out, "%s: %s\n", name, state)
		if cmd != "migrate" || !box.NeedsReseal(stored) {
			continue
		}
		if openErr != nil { // no se toca lo que no se puede leer
			failed++
			continue
		}
		if dry {
			fmt.Fprintf(out, "  would encrypt with key %s\n", box.KeyID())
			changed++
			continue
		}
		sealed, err := box.Seal(name, plain)
		if err != nil {
			return err
		}
		ok, err := store.ReplaceSetting(name, stored, sealed)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(out, "  changed meanwhile (saved again from Settings?): left as is, run migrate again\n")
			failed++
			continue
		}
		fmt.Fprintf(out, "  encrypted with key %s\n", box.KeyID())
		changed++
	}
	if cmd == "migrate" {
		fmt.Fprintf(out, "%d credential(s) migrated\n", changed)
		if failed > 0 {
			return fmt.Errorf("%d credential(s) could not be migrated: they were left unchanged", failed)
		}
	}
	return nil
}

func envFileName() string {
	if f := env.Get("ENV_FILE"); f != "" {
		return f
	}
	return ".env"
}
