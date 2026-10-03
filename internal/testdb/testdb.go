// Package testdb gives each test binary its own throwaway Postgres database
// with the core, payments and provisioner schemas, so packages that run in
// parallel under `go test ./...` never share (or truncate) each other's tables.
//
// BOBRES_TEST_DATABASE_URL is a URL-form DSN for any database on the server
// (default: the local socket in /tmp); its role needs CREATEDB. When the server
// is unreachable, tests skip, unless BOBRES_TEST_REQUIRE_DB=1 (set in CI), in
// which case they fail: a green CI run must mean the database tests ran.
//
// Use it from TestMain so the database is dropped when the binary exits:
//
//	func TestMain(m *testing.M) { testdb.Main(m) }
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultBase = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"

var (
	once     sync.Once
	dsn      string
	dbName   string
	baseDSN  string
	setupErr error
)

// Main runs the package's tests and drops the database afterwards.
func Main(m *testing.M) {
	code := m.Run()
	drop()
	os.Exit(code)
}

// DSN returns the DSN of this test binary's database, creating it on first use.
func DSN(t testing.TB) string {
	t.Helper()
	once.Do(setup)
	if setupErr != nil {
		if os.Getenv("BOBRES_TEST_REQUIRE_DB") == "1" {
			t.Fatalf("testdb: postgres required but unavailable: %v", setupErr)
		}
		t.Skipf("testdb: postgres unavailable: %v", setupErr)
	}
	return dsn
}

// Required reports whether tests must fail rather than skip when an external
// dependency is missing (CI sets BOBRES_TEST_REQUIRE_DB=1).
func Required() bool { return os.Getenv("BOBRES_TEST_REQUIRE_DB") == "1" }

func setup() {
	baseDSN = os.Getenv("BOBRES_TEST_DATABASE_URL")
	if baseDSN == "" {
		baseDSN = defaultBase
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		setupErr = err
		return
	}
	defer admin.Close(ctx) //nolint:errcheck // best-effort close of a test connection

	name := "bobres_test_" + randHex(6)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		setupErr = fmt.Errorf("create database: %w", err)
		return
	}
	dbName = name

	d, err := withDatabase(baseDSN, name)
	if err != nil {
		setupErr = err
		return
	}
	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		setupErr = err
		return
	}
	defer conn.Close(ctx) //nolint:errcheck // best-effort close of a test connection
	// Production creates these in deploy/postgres-init.sh; migrations expect them.
	if _, err := conn.Exec(ctx, `CREATE SCHEMA core; CREATE SCHEMA payments; CREATE SCHEMA provisioner;`); err != nil {
		setupErr = fmt.Errorf("create schemas: %w", err)
		return
	}
	dsn = d
}

func drop() {
	if dbName == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb: could not drop %s: %v\n", dbName, err)
		return
	}
	defer admin.Close(ctx) //nolint:errcheck // best-effort close of a test connection
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
		fmt.Fprintf(os.Stderr, "testdb: could not drop %s: %v\n", dbName, err)
	}
}

// withDatabase swaps the database name in a URL-form DSN.
func withDatabase(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("testdb: BOBRES_TEST_DATABASE_URL must be a postgres:// URL")
	}
	u.Path = "/" + name
	q := u.Query()
	q.Del("dbname")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(hex.EncodeToString(b))
}

// RedisAddr returns the Redis address for tests (BOBRES_TEST_REDIS_ADDR,
// default 127.0.0.1:6379). Callers skip when it is unreachable unless
// Required() is true.
func RedisAddr() string {
	if a := os.Getenv("BOBRES_TEST_REDIS_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:6379"
}
