package main

import (
	"context"
	"flag"
	"fmt"
)

const adminUsage = "usage: bobres admin link [--dir D] [telegram id]\n" +
	"       bobres admin password [--dir D]"

// adminCmd runs the dashboard sign-in helpers inside the core container:
//
//	bobres admin link [--dir D] [telegram id]   a one-time login link for the
//	    owner (or that staff member); it works once, within 2 minutes, and
//	    needs the owner to have started the bot once.
//	bobres admin password [--dir D]             a new random initial password
//	    for the owner, for signing in without Telegram at all. It is shown
//	    once; the first login must change it and set up two-factor
//	    authentication. Running it again resets the owner's login.
func (o *ops) adminCmd(args []string) int {
	if len(args) == 0 || (args[0] != "link" && args[0] != "password") {
		fmt.Fprintln(o.errOut, adminUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("admin "+sub, flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
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
		return o.adminPassword(*dir)
	}
	cargs := append(composeArgs("exec", "-T", "core", "/app", "login-link"), fs.Args()...)
	if err := o.stream(context.Background(), *dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: %v (is core running? bobres status)\n"+
			"No Telegram, or the owner never started the bot? Use: bobres admin password\n", err)
		return 1
	}
	return 0
}

// adminPassword resets the owner's dashboard password and prints the new one
// (core writes it to the terminal; nothing is logged or kept in a file).
func (o *ops) adminPassword(dir string) int {
	fmt.Fprintln(o.out, "A new initial password for the dashboard owner is shown below. It is shown once;\n"+
		"you must change it at first login (and set up two-factor authentication).\n"+
		"This signs out the owner's other dashboard sessions.")
	if err := o.stream(context.Background(), dir, composeArgs("exec", "-T", "core", "/app", "admin-password")...); err != nil {
		fmt.Fprintf(o.errOut, "error: %v (is core running? bobres status)\n", err)
		return 1
	}
	return 0
}
