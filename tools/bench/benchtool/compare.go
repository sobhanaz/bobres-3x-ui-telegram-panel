package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// compare prints two runs' headline numbers side by side (each run's
// summary.json, written by report), with the change from A to B. A change
// larger than both runs' own spread is marked, so noise is not read as a
// difference.
func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	a := fs.String("a", "", "the baseline run's directory (docs/bench/DATE/plain)")
	b := fs.String("b", "", "the other run's directory (docs/bench/DATE/garble)")
	out := fs.String("out", "", "also write the table to this Markdown file")
	match := fs.String("match", "", "only metrics whose key contains this")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *a == "" || *b == "" {
		return errors.New("usage: compare -a DIR -b DIR [-out F.md] [-match KEY]")
	}
	var sa, sb summaryFile
	if err := readJSON(filepath.Join(*a, "summary.json"), &sa); err != nil {
		return fmt.Errorf("%w (run benchtool report -dir %s first)", err, *a)
	}
	if err := readJSON(filepath.Join(*b, "summary.json"), &sb); err != nil {
		return fmt.Errorf("%w (run benchtool report -dir %s first)", err, *b)
	}
	bm := map[string]metric{}
	for _, m := range sb.Metrics {
		bm[m.Key] = m
	}
	var w io.Writer = os.Stdout
	var sbuf strings.Builder
	if *out != "" {
		w = io.MultiWriter(os.Stdout, &sbuf)
	}
	fmt.Fprintf(w, "| Metric | %s | %s | Change | |\n|---|---:|---:|---:|---|\n", label(sa, *a), label(sb, *b))
	seen := map[string]bool{}
	for _, ma := range sa.Metrics {
		if seen[ma.Key] || (*match != "" && !strings.Contains(ma.Key, *match)) {
			continue
		}
		seen[ma.Key] = true
		mb, ok := bm[ma.Key]
		if !ok {
			fmt.Fprintf(w, "| %s | %s %s | – | | only in A |\n", ma.Key, g(ma.Value), ma.Unit)
			continue
		}
		change, mark := "", ""
		if ma.Value != 0 {
			d := (mb.Value - ma.Value) / math.Abs(ma.Value)
			change = fmt.Sprintf("%+.1f%%", 100*d)
			// Beyond the noise: the change is larger than half of either run's spread.
			if noise := math.Max(ma.Spread, mb.Spread) / 2; math.Abs(d) > math.Max(noise, 0.02) {
				mark = "**"
			}
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s%s%s | |\n", ma.Key, g(ma.Value), g(mb.Value), mark, change, mark)
	}
	for _, m := range sb.Metrics {
		if !seen[m.Key] && (*match == "" || strings.Contains(m.Key, *match)) {
			fmt.Fprintf(w, "| %s | – | %s %s | | only in B |\n", m.Key, g(m.Value), m.Unit)
		}
	}
	if *out != "" {
		return os.WriteFile(*out, []byte(sbuf.String()), 0o644) //nolint:gosec // meant to be read
	}
	return nil
}

func label(s summaryFile, dir string) string {
	if s.Variant != "" {
		return s.Variant + " (" + filepath.Base(filepath.Dir(dir)) + ")"
	}
	return dir
}
