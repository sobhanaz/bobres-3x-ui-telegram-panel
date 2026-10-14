package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

const aboutUsage = "usage: bobres about [--dir D]"

// aboutCmd: `bobres about [--dir D]` prints the banner and what there is to know
// about this bobres in one place: its build, license and copyright, the vendor
// key that signs license tokens, the store installed here and where to go for
// documentation, support and security reports.
func aboutCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("about", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, aboutUsage)
		return 2
	}
	printAbout(out, *dir, !bannerOff(os.Getenv))
	return 0
}

// printAbout writes the information of `bobres about`, after the banner when
// banner is set.
func printAbout(out io.Writer, dir string, banner bool) {
	t := detectUI(out, os.Getenv)
	if banner {
		printBanner(out, t)
	}
	for _, l := range aboutLines(t, currentBuild(), dir) {
		fmt.Fprintln(out, l)
	}
}

// aboutLines is the information `bobres about` prints after the banner.
func aboutLines(t termUI, b buildInfo, dir string) []string {
	copyright := brandCopyright
	if !t.unicode {
		copyright = strings.ReplaceAll(copyright, "©", "(c)")
	}
	const indent = "            " // under the values
	var lines []string
	row := func(label, value string) {
		lines = append(lines, fmt.Sprintf("%s %s", t.paint(roleTeal, fmt.Sprintf("%-11s", label)), value))
	}
	more := func(value string) { lines = append(lines, indent+value) }

	row("Version", b.version)
	if b.commit != "" {
		row("Commit", b.commit)
	}
	if b.built != "" {
		row("Built", b.built)
	}
	row("Platform", runtime.GOOS+"/"+runtime.GOARCH+", "+runtime.Version())
	row("License", brandLicense)
	row("Copyright", copyright)
	row("Credit", brandMadeBy)
	if ring, err := license.Embedded(); err != nil {
		row("License key", "unreadable: "+err.Error())
	} else {
		kids := make([]string, 0, len(ring))
		for kid := range ring {
			kids = append(kids, kid)
		}
		sort.Strings(kids)
		for i, kid := range kids {
			if i == 0 {
				row("License key", kid+"  "+license.Fingerprint(ring[kid]))
			} else {
				more(kid + "  " + license.Fingerprint(ring[kid]))
			}
		}
		more("(the vendor key built into this bobres: a license token must be signed by it)")
	}
	row("Store", storeLine(dir))
	row("Website", brandSite)
	row("Docs", brandRepo)
	row("Issues", brandIssues)
	row("Security", brandSecurity)
	row("Notices", "THIRD_PARTY_NOTICES.md and LICENSE, in the repository (Docs, above)")
	lines = append(lines, "",
		"The LICENSE text is a draft: a lawyer has not reviewed it yet, so do not read it as",
		"legal advice or as the final terms. Questions about it, and support, go to Issues;",
		"a security problem goes to Security, never to a public issue.")
	return lines
}

// storeLine says whether a store is installed in dir.
func storeLine(dir string) string {
	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); err != nil {
		return "none found in " + dir + " (bobres install sets one up)"
	}
	if env, ok, err := readEnv(envPath); err == nil && ok {
		return fmt.Sprintf("%s (version %s)", dir, orDash(env["BOBRES_VERSION"]))
	}
	return dir + " (its settings are readable by root only: run sudo bobres about)"
}
