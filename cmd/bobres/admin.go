package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

const adminUsage = "usage: bobres admin link [--dir D] [telegram id]\n" +
	"       bobres admin password [--dir D] [--id <owner telegram id>]\n" +
	"       bobres admin owners list|add|remove [--dir D] [--no-restart] [telegram id]"

// adminCmd runs the dashboard sign-in helpers inside the core container:
//
//	bobres admin link [--dir D] [telegram id]   a one-time login link for the
//	    owner (or that staff member); it works once, within 2 minutes, and
//	    needs the owner to have started the bot once.
//	bobres admin password [--dir D] [--id N]    a new random initial password
//	    for the owner (the main owner, or the owner with Telegram id N), for
//	    signing in without Telegram at all. It is shown once; the first login
//	    must change it and set up two-factor authentication. Running it again
//	    resets that owner's login.
//	bobres admin owners list|add|remove         the owners the server names
//	    (BOBRES_ADMIN_TELEGRAM_ID): list them, add one or remove one.
func (o *ops) adminCmd(args []string) int {
	if len(args) == 0 || (args[0] != "link" && args[0] != "password" && args[0] != "owners") {
		fmt.Fprintln(o.errOut, adminUsage)
		return 2
	}
	sub := args[0]
	if sub == "owners" {
		return o.ownersCmd(args[1:])
	}
	fs := flag.NewFlagSet("admin "+sub, flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	var id *string
	if sub == "password" {
		id = fs.String("id", "", "Telegram id of the owner (default: the main owner, the first one listed)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if (sub == "link" && fs.NArg() > 1) || (sub == "password" && fs.NArg() > 0) {
		fmt.Fprintln(o.errOut, adminUsage)
		return 2
	}
	if _, ok := o.requireInstall(*dir); !ok {
		return 2
	}
	if sub == "password" {
		return o.adminPassword(*dir, *id)
	}
	cargs := append(composeArgs("exec", "-T", "core", "/app", "login-link"), fs.Args()...)
	if err := o.stream(context.Background(), *dir, cargs...); err != nil {
		var ec interface{ ExitCode() int }
		if errors.As(err, &ec) && ec.ExitCode() == coreExitNotStaff {
			// The owner becomes staff by starting the bot: say so, with the
			// bot's name, and name the way that needs no Telegram.
			fmt.Fprintln(o.errOut, notStaffAdvice(o.botName(context.Background(), *dir)))
			fmt.Fprintln(o.errOut, "No Telegram at all? Use: bobres admin password")
			return 1
		}
		fmt.Fprintf(o.errOut, "error: %v (is core running? bobres status)\n"+
			"No Telegram, or the owner never started the bot? Use: bobres admin password\n", err)
		return 1
	}
	return 0
}

// adminPassword resets an owner's dashboard password and prints the new one
// (core writes it to the terminal; nothing is logged or kept in a file). id
// is the owner's Telegram id, "" for the main owner.
func (o *ops) adminPassword(dir, id string) int {
	cargs := composeArgs("exec", "-T", "core", "/app", "admin-password")
	if id != "" {
		if n, err := strconv.ParseInt(id, 10, 64); err != nil || n <= 0 {
			fmt.Fprintln(o.errOut, "error: --id is the owner's Telegram id, a positive number (bobres admin owners list)")
			return 2
		}
		cargs = append(cargs, "--id", id)
	}
	fmt.Fprintln(o.out, "A new initial password for the dashboard owner is shown below. It is shown once;\n"+
		"you must change it at first login (and set up two-factor authentication).\n"+
		"This signs out the owner's other dashboard sessions.")
	if err := o.stream(context.Background(), dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: %v (is core running? bobres status)\n", err)
		return 1
	}
	return 0
}

// ownersCmd lists, adds and removes the owners the server names
// (BOBRES_ADMIN_TELEGRAM_ID, ids separated by commas): the store's root
// admins. They are changed here, from the server, and nowhere else: the
// dashboard and the bot cannot make or unmake one, so a hijacked Telegram or
// dashboard account never becomes a permanent owner.
func (o *ops) ownersCmd(args []string) int {
	fs := flag.NewFlagSet("admin owners", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	noRestart := fs.Bool("no-restart", false, "add, remove: only write .env; the change applies when the store next starts")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 || (pos[0] == "list" && len(pos) != 1) || ((pos[0] == "add" || pos[0] == "remove") && len(pos) != 2) ||
		(pos[0] != "list" && pos[0] != "add" && pos[0] != "remove") {
		fmt.Fprintln(o.errOut, adminUsage)
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	owners, err := config.ParseTelegramIDs(env["BOBRES_ADMIN_TELEGRAM_ID"])
	if err != nil {
		fmt.Fprintf(o.errOut, "error: BOBRES_ADMIN_TELEGRAM_ID in .env is not a list of Telegram ids: %v\n", err)
		return 1
	}
	if pos[0] == "list" {
		return o.ownersList(owners)
	}
	id, err := strconv.ParseInt(pos[1], 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintln(o.errOut, "error: the Telegram id is a positive number (ask @userinfobot). Nothing changed.")
		return 2
	}
	next, outcome := ownersChange(o, pos[0], id, owners)
	switch outcome {
	case ownersRefused:
		return 1
	case ownersNothingToDo:
		return 0
	}
	if code := o.configSet(*dir, "BOBRES_ADMIN_TELEGRAM_ID", next, *noRestart); code != 0 {
		return code
	}
	if pos[0] == "add" {
		fmt.Fprintf(o.out, "\nTelegram id %d is named as an owner. They open the bot and send /start once, or you run:  bobres admin password --id %d\n"+
			"Only the main owner (%d) takes no staff seat: this one takes a seat like any other staff member.\n", id, id, firstID(next))
		return 0
	}
	fmt.Fprintf(o.out, "\nTelegram id %d is no longer named in the server settings. If they already signed in, they keep the owner role\n"+
		"until you demote or remove them in the dashboard (Staff): removing an id here never locks anyone out by itself.\n", id)
	return 0
}

// firstID is the main owner in a comma-separated list of ids (0: none).
func firstID(list string) int64 {
	ids, _ := config.ParseTelegramIDs(list)
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// What ownersChange decided.
type ownersOutcome int

const (
	ownersChanged     ownersOutcome = iota // write the new list
	ownersRefused                          // it said why on stderr: exit 1
	ownersNothingToDo                      // it said so: exit 0
)

// ownersChange is the new BOBRES_ADMIN_TELEGRAM_ID after adding or removing
// id, with what to do about it.
func ownersChange(o *ops, action string, id int64, owners []int64) (string, ownersOutcome) {
	switch action {
	case "add":
		switch {
		case slices.Contains(owners, id):
			fmt.Fprintf(o.out, "Telegram id %d is an owner already: nothing to change.\n", id)
			return "", ownersNothingToDo
		case len(owners) >= config.MaxAdminTelegramIDs:
			fmt.Fprintf(o.errOut, "error: at most %d owners may be named. Nothing changed.\n", config.MaxAdminTelegramIDs)
			return "", ownersRefused
		}
		owners = append(slices.Clone(owners), id)
	case "remove":
		switch {
		case !slices.Contains(owners, id):
			fmt.Fprintf(o.errOut, "error: Telegram id %d is not an owner named in the server settings (bobres admin owners list). Nothing changed.\n", id)
			return "", ownersRefused
		case len(owners) == 1:
			fmt.Fprintln(o.errOut, "error: that is the only owner: a store needs one. Add the new owner first (bobres admin owners add <id>). Nothing changed.")
			return "", ownersRefused
		}
		owners = slices.DeleteFunc(slices.Clone(owners), func(x int64) bool { return x == id })
	}
	parts := make([]string, len(owners))
	for i, x := range owners {
		parts[i] = strconv.FormatInt(x, 10)
	}
	return strings.Join(parts, ","), ownersChanged
}

// ownersList prints the owners the server names, the main owner first.
func (o *ops) ownersList(owners []int64) int {
	if len(owners) == 0 {
		fmt.Fprintln(o.out, "No owner is named in the server settings (BOBRES_ADMIN_TELEGRAM_ID is empty): bobres admin owners add <id>")
		return 0
	}
	fmt.Fprintln(o.out, "Owners named in the server settings (BOBRES_ADMIN_TELEGRAM_ID):")
	for i, id := range owners {
		role := "takes a staff seat"
		if i == 0 {
			role = "main owner: takes no staff seat, gets the first dashboard password"
		}
		fmt.Fprintf(o.out, "  %d. %d   (%s)\n", i+1, id, role)
	}
	fmt.Fprintln(o.out, "Each is made an owner when core starts or when they send /start to the bot. Add or remove one: bobres admin owners add|remove <id>")
	return 0
}
