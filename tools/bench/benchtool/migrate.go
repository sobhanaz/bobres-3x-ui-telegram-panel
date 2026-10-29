package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
)

type migrateRun struct {
	Schema map[string]float64 `json:"schema_s"`    // empty database to the latest version
	TotalS float64            `json:"total_s"`     // all three
	NoopS  float64            `json:"noop_s"`      // Up again on the migrated database: what every restart pays
	Load   []float64          `json:"load_before"` // load average before the run
}

type migrateResult struct {
	Kind   string           `json:"kind"`
	Files  map[string]int   `json:"files"` // migration files per schema
	Runs   []migrateRun     `json:"runs"`
	Total  stats            `json:"total_s"`
	Noop   stats            `json:"noop_s"`
	Schema map[string]stats `json:"schema_s"`
}

// migrateCmd times every migration on an empty database, -repeat times, a
// fresh database each time.
func migrateCmd(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	out := fs.String("out", "", "JSON result")
	repeat := fs.Int("repeat", 3, "runs, each on a fresh database")
	base := fs.String("db", envOr("BOBRES_TEST_DATABASE_URL", defaultBaseDSN), "a postgres:// URL of a role that may CREATE DATABASE")
	src := fs.String("src", "internal/migrate/migrations", "the migrations, to count them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("usage: migrate -out F.json [-repeat 3]")
	}
	goose.SetLogger(goose.NopLogger()) // one line per migration, three times over
	ctx := context.Background()
	res := migrateResult{Kind: "migrate", Files: map[string]int{}, Schema: map[string]stats{}}
	for _, s := range migrate.Schemas() {
		m, _ := filepath.Glob(filepath.Join(*src, s, "*.sql"))
		res.Files[s] = len(m)
	}
	perSchema := map[string][]float64{}
	var totals, noops []float64
	for i := range *repeat {
		dsn, drop, err := benchDB(ctx, *base)
		if err != nil {
			return err
		}
		run := migrateRun{Schema: map[string]float64{}, Load: loadAvg()}
		begin := time.Now()
		for _, s := range migrate.Schemas() {
			t := time.Now()
			if err := migrate.Up(ctx, dsn, s); err != nil {
				drop()
				return fmt.Errorf("migrate %s: %w", s, err)
			}
			run.Schema[s] = time.Since(t).Seconds()
			perSchema[s] = append(perSchema[s], run.Schema[s])
		}
		run.TotalS = time.Since(begin).Seconds()
		t := time.Now()
		for _, s := range migrate.Schemas() {
			if err := migrate.Up(ctx, dsn, s); err != nil {
				drop()
				return err
			}
		}
		run.NoopS = time.Since(t).Seconds()
		drop()
		totals, noops = append(totals, run.TotalS), append(noops, run.NoopS)
		res.Runs = append(res.Runs, run)
		fmt.Fprintf(os.Stderr, "  migrations #%d: %.2fs (%s), again %.3fs, load %v\n", i+1, run.TotalS,
			fmtSchemas(run.Schema), run.NoopS, run.Load)
	}
	res.Total, res.Noop = summarize(totals), summarize(noops)
	for s, v := range perSchema {
		res.Schema[s] = summarize(v)
	}
	return writeJSON(*out, res)
}

func fmtSchemas(m map[string]float64) string {
	var parts []string
	for _, s := range migrate.Schemas() {
		parts = append(parts, fmt.Sprintf("%s %.2fs", s, m[s]))
	}
	return strings.Join(parts, ", ")
}
