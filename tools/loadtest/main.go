// Command loadtest drives core's gRPC API the way the bot does, with many
// customers at once, and reports throughput, p50/p95/p99 per call and errors
// (docs/12-load-test.md). Each simulated customer runs journey after journey:
// /start, registering, an admin's wallet top-up, the plan list, paying from
// the wallet, their services and a support ticket, each journey as a new
// Telegram user.
//
// The usual way runs the whole backend in-process (internal/testenv: core,
// payments and provisioner with a fake 3x-ui panel) on the test Postgres,
// with a fresh stack for each user count:
//
//	go test ./tools/loadtest -run TestLocalStack -count=1 -timeout 60m -v -args -users 50,200,500 -duration 30s
//
// Or it connects to a core that is already running, such as a development
// stack started by hand, with the bot's service token in
// BOBRES_LOADTEST_TOKEN:
//
//	BOBRES_LOADTEST_TOKEN=... go run ./tools/loadtest -addr 127.0.0.1:9090 -owner 123456 -users 200 -duration 30s
//
// It writes customers, orders, payments, tickets and VPN accounts on the
// stack's panel, so it refuses an address that is not on this machine unless
// -force is given. Never point it at a production install.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
)

// tokenEnv holds the token core accepts from the bot.
const tokenEnv = "BOBRES_LOADTEST_TOKEN" //nolint:gosec // the variable's name, not a credential

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(errOut)
	addr := fs.String("addr", "", "core's gRPC address, e.g. 127.0.0.1:9090 (token in "+tokenEnv+")")
	owner := fs.Int64("owner", 0, "Telegram id of the owner or an admin, who tops the wallets up")
	users := fs.Int("users", 50, "simulated customers at the same time")
	cfg := flags(fs)
	force := fs.Bool("force", false, "allow an address that is not on this machine")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	token := getenv(tokenEnv)
	if *addr == "" || *owner == 0 || token == "" || fs.NArg() > 0 {
		fmt.Fprintln(errOut, "usage: "+tokenEnv+"=... loadtest -addr HOST:PORT -owner TELEGRAM_ID [-users N] [-duration D] [-think D]")
		fmt.Fprintln(errOut, "(the in-process stack runs with go test, see docs/12-load-test.md)")
		return 2
	}
	if err := local(*addr); err != nil && !*force {
		fmt.Fprintln(errOut, "loadtest:", err, "(pass -force to use it anyway; never a production install)")
		return 2
	}
	cfg.Users, cfg.Owner = *users, *owner
	// Far above real Telegram ids, and 10 million apart for runs a second
	// apart, so a run does not meet an earlier run's customers.
	cfg.TGBase = 7_000_000_000_000 + time.Now().Unix()%100_000*10_000_000

	conn, err := grpcx.Dial(*addr, token)
	if err != nil {
		fmt.Fprintln(errOut, "loadtest:", err)
		return 1
	}
	defer conn.Close() //nolint:errcheck // process exit
	res, err := load(ctx, corev1.NewCoreServiceClient(conn), *cfg)
	if res.Rec != nil {
		writeReport(out, res)
	}
	if err != nil {
		fmt.Fprintln(errOut, "loadtest:", err)
		return 1
	}
	return 0
}

// flags registers the settings both ways of running share (each has its own
// -users).
func flags(fs *flag.FlagSet) *config {
	c := &config{}
	fs.DurationVar(&c.Duration, "duration", 30*time.Second, "how long new journeys are started")
	fs.DurationVar(&c.Think, "think", 0, "pause between a customer's bot updates, give or take half (0: as fast as core answers)")
	fs.DurationVar(&c.Timeout, "timeout", 10*time.Second, "per call")
	return c
}

// load sets up, runs and cleans up one run.
func load(ctx context.Context, core corev1.CoreServiceClient, cfg config) (result, error) {
	setup, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	r, err := newRunner(setup, core, cfg)
	if err != nil {
		return result{}, err
	}
	res := r.start(ctx)
	done, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.close(done); err != nil {
		return res, fmt.Errorf("switch the load test plan off: %w", err)
	}
	return res, nil
}

// local refuses an address that is not a loopback one: a load test writes
// thousands of customers and orders.
func local(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return fmt.Errorf("%s is not on this machine (%s)", host, ip)
		}
	}
	if len(ips) == 0 {
		return errors.New("no address for " + host)
	}
	return nil
}
