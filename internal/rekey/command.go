package rekey

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

const usage = "usage: rekey [--dry-run]"

// Command is the `rekey` subcommand every service with encrypted values has
// (core, payments, provisioner): it reads the service's database URL and keys
// from its environment, and seals again what the current key does not read.
// `bobres rotate-key` runs it in each service's container. Exit 0 when done (or
// nothing to do), 1 when it failed or found a value no key reads (nothing is
// written then), 2 for usage errors.
func Command(service string, args []string, out, errOut io.Writer) int {
	return command(service, args, out, errOut, openFromEnv)
}

// openFunc gives the database and the envelope of the service, and a function that closes the database.
type openFunc func(ctx context.Context) (DB, *crypto.Envelope, func(), error)

// openFromEnv connects with the service's own database role and builds its envelope from its environment.
func openFromEnv(ctx context.Context) (DB, *crypto.Envelope, func(), error) {
	cfg, err := config.LoadKeyConfig()
	if err != nil {
		return nil, nil, nil, err
	}
	if cfg.MasterKey == "" {
		return nil, nil, func() {}, nil
	}
	env, err := crypto.NewKeyring(cfg.KeyExtras.Keyring(cfg.MasterKey))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("the keys: %w", err)
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, errors.New("the database URL is not usable")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, nil, fmt.Errorf("connect to the database: %w", err)
	}
	return pool, env, pool.Close, nil
}

func command(service string, args []string, out, errOut io.Writer, open openFunc) int {
	fs := flag.NewFlagSet("rekey", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dry := fs.Bool("dry-run", false, "count and check every encrypted value, and write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tables, ok := Tables[service]
	if fs.NArg() != 0 || !ok {
		fmt.Fprintln(errOut, usage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, env, closeDB, err := open(ctx)
	if err != nil {
		fmt.Fprintf(errOut, "error: %s: %v\n", service, err)
		return 1
	}
	defer closeDB()
	if env == nil {
		fmt.Fprintf(out, "%s: no master key is set, so nothing is encrypted: nothing to do.\n", service)
		return 0
	}
	rep, err := Run(ctx, db, env, tables, Options{DryRun: *dry})
	Print(out, service, env, rep, err == nil)
	if err != nil {
		fmt.Fprintf(errOut, "error: %s: %v\n", service, err)
		if *dry || rep.DryRun {
			fmt.Fprintf(errOut, "%s: nothing was written.\n", service)
		}
		return 1
	}
	return 0
}

// Print says what a run found and did, table by table. It names no value.
func Print(w io.Writer, service string, env *crypto.Envelope, rep Report, ok bool) {
	prev := "no previous key"
	if env.HasPrevious() {
		prev = "a previous key is accepted"
	}
	fmt.Fprintf(w, "%s: key %s, %s\n", service, env.KeyID(), prev)
	verb := "re-encrypted"
	if rep.DryRun {
		verb = "to re-encrypt"
	}
	for _, t := range rep.Tables {
		fmt.Fprintf(w, "  %-34s %d rows, %d encrypted values, %d on the current key, %d %s\n", t.Table, t.Rows, t.Values, t.OnCurrent, t.Sealed, verb)
	}
	if !ok {
		return
	}
	if rep.DryRun {
		fmt.Fprintf(w, "%s: dry run, nothing was written: %d of %d values are not on the current key yet.\n", service, rep.Sealed(), rep.Values())
		return
	}
	fmt.Fprintf(w, "%s: %d of %d values re-encrypted; every value now reads with the current key alone.\n", service, rep.Sealed(), rep.Values())
}
