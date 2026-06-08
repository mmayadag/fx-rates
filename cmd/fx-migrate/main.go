// Command fx-migrate applies or rolls back database migrations independently
// of the main sync job. The sync binary still runs migrations on startup when
// RUN_MIGRATIONS=true; this tool exists for operators who want to manage
// schema changes out-of-band (e.g. apply migrations once, then run the job
// with RUN_MIGRATIONS=false).
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"

	"github.com/mmayadag/fx-rates/internal/config"
	"github.com/mmayadag/fx-rates/internal/db"
)

// dsnCredentialsRe masks the "user:password@" portion of a DSN before an
// error string (which golang-migrate may embed the full DSN into) is printed.
var dsnCredentialsRe = regexp.MustCompile(`(?i)(postgres(?:ql)?|pgx5?)://[^\s:/@]+:[^\s@/]+@`)

func redactDSN(s string) string {
	return dsnCredentialsRe.ReplaceAllString(s, "$1://***:***@")
}

func main() {
	var (
		down    = flag.Bool("down", false, "roll back the most recently applied migration instead of applying all pending ones")
		envFile = flag.String("env-file", ".env", "path to env file")
	)
	flag.Parse()

	cfg, err := config.Bootstrap(*envFile)
	if err != nil {
		exitErr(err)
	}

	if *down {
		if err := db.RollbackMigration(cfg.DatabaseURL); err != nil {
			exitErr(fmt.Errorf("rollback failed: %s", redactDSN(err.Error())))
		}
		fmt.Fprintln(os.Stderr, "rolled back one migration")
		return
	}

	if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
		exitErr(fmt.Errorf("migrations failed: %s", redactDSN(err.Error())))
	}
	fmt.Fprintln(os.Stderr, "migrations applied")
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
