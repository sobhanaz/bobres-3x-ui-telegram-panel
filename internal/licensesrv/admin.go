package licensesrv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// KeyUsage is the `licensesrv key` help.
const KeyUsage = `usage:
  licensesrv key create -customer NAME -tier trial|standard|pro -entitlements FILE
                        (-ends-at YYYY-MM-DD | -days N | -no-end) [-grace-days 14]
  licensesrv key revoke ID -reason TEXT
  licensesrv key list [-status active|revoked]
  licensesrv key show ID
  licensesrv key update ID -reason TEXT [-customer NAME] [-tier T] [-entitlements FILE]
                        [-ends-at YYYY-MM-DD | -days N | -no-end] [-grace-days N]

The entitlements FILE is JSON as in docs/05-license.md ("-" reads stdin).
-ends-at YYYY-MM-DD ends the license at 00:00 UTC that day (or give RFC3339).
create prints the activation key once on stdout; only its hash is kept.
`

// AdminOptions configure the admin commands.
type AdminOptions struct {
	Now   func() time.Time
	Actor string // who runs them, for the audit (e.g. "cli:alice")
}

// errUsage means the arguments were wrong (exit code 2).
var errUsage = errors.New("usage")

// RunKey runs `licensesrv key <sub> ...` and returns the exit code.
func RunKey(ctx context.Context, st *Store, args []string, in io.Reader, out, errOut io.Writer, o AdminOptions) int {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Actor == "" {
		o.Actor = "cli"
	}
	a := &admin{st: st, in: in, out: out, errOut: errOut, now: o.Now, actor: o.Actor}
	if len(args) == 0 {
		fmt.Fprint(errOut, KeyUsage)
		return 2
	}
	var err error
	switch args[0] {
	case "create":
		err = a.create(ctx, args[1:])
	case "revoke":
		err = a.revoke(ctx, args[1:])
	case "list":
		err = a.list(ctx, args[1:])
	case "show":
		err = a.show(ctx, args[1:])
	case "update":
		err = a.update(ctx, args[1:])
	default:
		err = errUsage
	}
	switch {
	case errors.Is(err, errUsage), errors.Is(err, flag.ErrHelp):
		fmt.Fprint(errOut, KeyUsage)
		return 2
	case err != nil:
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

type admin struct {
	st          *Store
	in          io.Reader
	out, errOut io.Writer
	now         func() time.Time
	actor       string
}

// endFlags are the three ways to say when a license ends.
type endFlags struct {
	endsAt string
	days   int
	noEnd  bool
}

func (e *endFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&e.endsAt, "ends-at", "", "when the license ends: YYYY-MM-DD (00:00 UTC) or an RFC3339 time")
	fs.IntVar(&e.days, "days", 0, "valid for N days from now")
	fs.BoolVar(&e.noEnd, "no-end", false, "the license never ends")
}

// set reports whether any was given; resolve turns them into ends_at.
func (e *endFlags) set() bool { return e.endsAt != "" || e.days != 0 || e.noEnd }

func (e *endFlags) resolve(now time.Time) (*time.Time, error) {
	n := 0
	for _, b := range []bool{e.endsAt != "", e.days != 0, e.noEnd} {
		if b {
			n++
		}
	}
	if n != 1 {
		return nil, errors.New("give exactly one of -ends-at, -days and -no-end")
	}
	switch {
	case e.noEnd:
		return nil, nil
	case e.days != 0:
		if e.days < 0 || e.days > 36500 {
			return nil, errors.New("-days must be 1..36500")
		}
		t := now.UTC().Truncate(time.Second).Add(time.Duration(e.days) * 24 * time.Hour)
		return &t, nil
	}
	if d, err := time.Parse(time.DateOnly, e.endsAt); err == nil {
		return &d, nil
	}
	t, err := time.Parse(time.RFC3339, e.endsAt)
	if err != nil {
		return nil, fmt.Errorf("-ends-at %q: want YYYY-MM-DD or RFC3339", e.endsAt)
	}
	t = t.UTC()
	return &t, nil
}

func (a *admin) create(ctx context.Context, args []string) error {
	fs := a.flagSet("key create")
	customer := fs.String("customer", "", "the customer's name")
	tier := fs.String("tier", "", "trial, standard or pro")
	entFile := fs.String("entitlements", "", "entitlements JSON file (\"-\": stdin)")
	grace := fs.Int("grace-days", DefaultGraceDays, "days the install keeps working after a token expires (0..90)")
	var ends endFlags
	ends.register(fs)
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || *entFile == "" {
		return errUsage
	}
	now := a.now().UTC()
	l := License{Customer: strings.TrimSpace(*customer), Tier: *tier, GraceDays: *grace, CreatedAt: now, UpdatedAt: now}
	if l.EndsAt, err = ends.resolve(now); err != nil {
		return err
	}
	if l.Entitlements, err = a.readEntitlements(*entFile); err != nil {
		return err
	}
	if err := checkLicense(l, now, true); err != nil {
		return err
	}
	if l.ID, err = newLicenseID(); err != nil {
		return err
	}
	key, err := NewKey()
	if err != nil {
		return err
	}
	err = a.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := a.st.CreateLicense(ctx, tx, l, HashKey(key)); err != nil {
			return err
		}
		return a.st.AddAudit(ctx, tx, AuditEntry{At: now, Actor: a.actor, Action: "license.create", LicenseID: l.ID, Details: licenseDetails(l)})
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.errOut, "Created %s for %s (%s, %s).\n", l.ID, l.Customer, l.Tier, endsText(l.EndsAt))
	fmt.Fprintln(a.errOut, "The activation key is shown only now (the server keeps only its hash). Send it to the customer privately:")
	fmt.Fprintln(a.out, key)
	return nil
}

func (a *admin) revoke(ctx context.Context, args []string) error {
	fs := a.flagSet("key revoke")
	reason := fs.String("reason", "", "why (kept in the audit)")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUsage
	}
	why := strings.TrimSpace(*reason)
	if why == "" || len(why) > 500 {
		return errors.New("-reason is required (at most 500 characters)")
	}
	id, now := rest[0], a.now().UTC()
	err = a.st.WithTx(ctx, func(tx pgx.Tx) error {
		l, err := a.st.LicenseByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if l.Status == StatusRevoked {
			return fmt.Errorf("%s was already revoked on %s", id, l.RevokedAt.UTC().Format(time.DateOnly))
		}
		if err := a.st.RevokeLicense(ctx, tx, id, why, now); err != nil {
			return err
		}
		return a.st.AddAudit(ctx, tx, AuditEntry{At: now, Actor: a.actor, Action: "license.revoke", LicenseID: id, Details: map[string]any{"reason": why}})
	})
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("no license %s", id)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Revoked %s. It can no longer be activated or refreshed: its install keeps its last token until\nthat expires and its grace ends, then new actions are gated. Nothing on the install is deleted.\n", id)
	return nil
}

func (a *admin) list(ctx context.Context, args []string) error {
	fs := a.flagSet("key list")
	status := fs.String("status", "", "only active or revoked licenses")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 || (*status != "" && *status != StatusActive && *status != StatusRevoked) {
		return errUsage
	}
	now := a.now().UTC()
	ls, err := a.st.ListLicenses(ctx, a.st.db, *status, now.Add(-ReactivationWindow))
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCUSTOMER\tTIER\tSTATUS\tENDS\tACTIVE ON\tLAST SEEN\tMOVES (1y)")
	for _, l := range ls {
		on, seen := "-", "-"
		if l.ActiveDomain != "" {
			on = l.ActiveDomain
		}
		if l.LastSeenAt != nil {
			seen = l.LastSeenAt.UTC().Format(time.DateOnly)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d/%d\n", l.ID, oneLine(l.Customer), l.Tier, l.Status, endsDate(l.EndsAt), on, seen, l.Moves, MaxReactivations)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(ls) == 0 {
		fmt.Fprintln(a.errOut, "No licenses yet: make one with `licensesrv key create`.")
	}
	return nil
}

func (a *admin) show(ctx context.Context, args []string) error {
	fs := a.flagSet("key show")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUsage
	}
	q := a.st.db
	l, err := a.st.LicenseByID(ctx, q, rest[0], false)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("no license %s", rest[0])
	}
	if err != nil {
		return err
	}
	now := a.now().UTC()
	moves, _, err := a.st.CountedSince(ctx, q, l.ID, now.Add(-ReactivationWindow))
	if err != nil {
		return err
	}
	acts, err := a.st.Activations(ctx, q, l.ID, 20)
	if err != nil {
		return err
	}
	audit, err := a.st.Audit(ctx, q, l.ID, 20)
	if err != nil {
		return err
	}
	ent, err := json.MarshalIndent(l.Entitlements, "  ", "  ")
	if err != nil {
		return err
	}
	w := a.out
	fmt.Fprintf(w, "License:      %s\n", l.ID)
	fmt.Fprintf(w, "Customer:     %s\n", oneLine(l.Customer))
	fmt.Fprintf(w, "Tier:         %s\n", l.Tier)
	fmt.Fprintf(w, "Status:       %s\n", l.Status)
	if l.Status == StatusRevoked {
		fmt.Fprintf(w, "Revoked:      %s (%s)\n", l.RevokedAt.UTC().Format(time.RFC3339), oneLine(l.Reason))
	}
	fmt.Fprintf(w, "Ends:         %s\n", endsText(l.EndsAt))
	fmt.Fprintf(w, "Grace:        %d days\n", l.GraceDays)
	fmt.Fprintf(w, "Created:      %s\n", l.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "Moves (1y):   %d of %d\n", moves, MaxReactivations)
	fmt.Fprintf(w, "Entitlements:\n  %s\n", ent)
	fmt.Fprintln(w, "\nActivations (newest first):")
	if len(acts) == 0 {
		fmt.Fprintln(w, "  none yet")
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, act := range acts {
		state := "active"
		if act.DeactivatedAt != nil {
			state = "ended " + act.DeactivatedAt.UTC().Format(time.DateOnly)
		}
		kind := "first"
		if act.Counted {
			kind = "move"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\tlast seen %s\t%s\t%s\n", act.ActivatedAt.UTC().Format(time.DateOnly), act.Domain, act.InstallID,
			kind, act.LastSeenAt.UTC().Format(time.DateOnly), orDash(act.LastVersion), state)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nAudit (newest first):")
	for _, e := range audit {
		details, _ := json.Marshal(e.Details)
		fmt.Fprintf(w, "  %s  %-20s %-16s %s %s\n", e.At.UTC().Format(time.RFC3339), e.Action, e.Actor, orDash(e.IP), details)
	}
	return nil
}

func (a *admin) update(ctx context.Context, args []string) error {
	fs := a.flagSet("key update")
	reason := fs.String("reason", "", "why (kept in the audit)")
	customer := fs.String("customer", "", "the customer's name")
	tier := fs.String("tier", "", "trial, standard or pro")
	entFile := fs.String("entitlements", "", "entitlements JSON file (\"-\": stdin)")
	grace := fs.Int("grace-days", DefaultGraceDays, "days the install keeps working after a token expires (0..90)")
	var ends endFlags
	ends.register(fs)
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errUsage
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	why := strings.TrimSpace(*reason)
	if why == "" || len(why) > 500 {
		return errors.New("-reason is required (at most 500 characters)")
	}
	id, now := rest[0], a.now().UTC()
	var ent *license.Entitlements
	if *entFile != "" {
		e, err := a.readEntitlements(*entFile)
		if err != nil {
			return err
		}
		ent = &e
	}
	var before, after License
	err = a.st.WithTx(ctx, func(tx pgx.Tx) error {
		l, err := a.st.LicenseByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if l.Status == StatusRevoked {
			return fmt.Errorf("%s is revoked; a revoked license does not change (make a new key instead)", id)
		}
		before, after = *l, *l
		if given["customer"] {
			after.Customer = strings.TrimSpace(*customer)
		}
		if given["tier"] {
			after.Tier = *tier
		}
		if ent != nil {
			after.Entitlements = *ent
		}
		if given["grace-days"] {
			after.GraceDays = *grace
		}
		if ends.set() {
			if after.EndsAt, err = ends.resolve(now); err != nil {
				return err
			}
		}
		if err := checkLicense(after, now, ends.set()); err != nil {
			return err
		}
		after.UpdatedAt = now
		if err := a.st.UpdateLicense(ctx, tx, after); err != nil {
			return err
		}
		return a.st.AddAudit(ctx, tx, AuditEntry{At: now, Actor: a.actor, Action: "license.update", LicenseID: id, Details: map[string]any{
			"reason": why, "before": licenseDetails(before), "after": licenseDetails(after),
		}})
	})
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("no license %s", id)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Updated %s (%s, %s). Its install picks the change up at its next daily refresh.\n", id, after.Tier, endsText(after.EndsAt))
	return nil
}

// checkLicense checks what a token made from l would carry; newEnd also
// refuses an end in the past (an update that leaves the end alone may touch
// a license that has already ended).
func checkLicense(l License, now time.Time, newEnd bool) error {
	var errs []error
	if l.Customer == "" || len(l.Customer) > 200 {
		errs = append(errs, errors.New("-customer is required (at most 200 characters)"))
	}
	switch l.Tier {
	case license.TierTrial, license.TierStandard, license.TierPro:
	default:
		errs = append(errs, fmt.Errorf("-tier must be trial, standard or pro, not %q", l.Tier))
	}
	if l.GraceDays < 0 || l.GraceDays > 90 {
		errs = append(errs, errors.New("-grace-days must be 0..90"))
	}
	if newEnd && l.EndsAt != nil && !l.EndsAt.After(now) {
		errs = append(errs, errors.New("the license would end in the past"))
	}
	if err := checkEntitlements(l.Entitlements); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

var gatewayRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

func checkEntitlements(e license.Entitlements) error {
	var errs []error
	if e.MaxServers < license.Unlimited || e.MaxActiveServices < license.Unlimited || e.DashboardStaffSeat < license.Unlimited {
		errs = append(errs, errors.New("entitlements: limits must be -1 (unlimited) or more"))
	}
	for _, g := range e.Gateways {
		if !gatewayRe.MatchString(g) {
			errs = append(errs, fmt.Errorf("entitlements: gateway %q is not a gateway name", g))
		}
	}
	if e.UpdatesUntil != "" {
		if _, err := time.Parse(time.DateOnly, e.UpdatesUntil); err != nil {
			errs = append(errs, fmt.Errorf("entitlements: updates_until %q is not YYYY-MM-DD", e.UpdatesUntil))
		}
	}
	return errors.Join(errs...)
}

// readEntitlements reads the JSON strictly, so a misspelt field fails
// instead of granting nothing.
func (a *admin) readEntitlements(path string) (license.Entitlements, error) {
	var e license.Entitlements
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(a.in, 1<<20))
	} else {
		raw, err = os.ReadFile(path) //nolint:gosec // the vendor names the file
	}
	if err != nil {
		return e, fmt.Errorf("entitlements: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return e, fmt.Errorf("entitlements: %w", err)
	}
	return e, nil
}

// licenseDetails is what the audit keeps of a license (never the key).
func licenseDetails(l License) map[string]any {
	d := map[string]any{"customer": l.Customer, "tier": l.Tier, "grace_days": l.GraceDays, "entitlements": l.Entitlements, "ends_at": nil}
	if l.EndsAt != nil {
		d["ends_at"] = l.EndsAt.UTC().Format(time.RFC3339)
	}
	return d
}

// flagSet is a sub-command's flags: a bad flag is reported in one line,
// then RunKey prints KeyUsage.
func (a *admin) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	fs.Usage = func() {}
	return fs
}

// parseInterspersed parses flags placed before or after positional
// arguments ("revoke lic_x -reason ..." and "revoke -reason ... lic_x").
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %w", errUsage, err)
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func endsText(t *time.Time) string {
	if t == nil {
		return "no end date"
	}
	return "ends " + t.UTC().Format(time.RFC3339)
}

func endsDate(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format(time.DateOnly)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// oneLine keeps a stored name from breaking the table or the terminal.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}
