package main

import (
	"bufio"
	"errors"
	"flag"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type goBench struct {
	Pkg      string    `json:"pkg"`
	Name     string    `json:"name"`
	NsOp     []float64 `json:"ns_op_runs"`
	Ns       stats     `json:"ns_op"`
	BytesOp  float64   `json:"bytes_op"`
	AllocsOp float64   `json:"allocs_op"`
}

type goBenchResult struct {
	Kind    string    `json:"kind"`
	Benches []goBench `json:"benchmarks"`
}

var benchLine = regexp.MustCompile(`^(Benchmark\S+?)(-\d+)?\s+\d+\s+([\d.]+) ns/op(?:\s+([\d.]+) B/op)?(?:\s+([\d.]+) allocs/op)?`)

// gobench turns `go test -bench -benchmem -count N` output into JSON: the
// median ns/op of the N runs and its spread, B/op and allocs/op.
func gobench(args []string) error {
	fs := flag.NewFlagSet("gobench", flag.ContinueOnError)
	in := fs.String("in", "", "go test -bench output")
	out := fs.String("out", "", "JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		return errors.New("usage: gobench -in bench.txt -out F.json")
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read only
	byKey := map[string]*goBench{}
	var order []string
	pkg := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "pkg: "); ok {
			pkg = strings.TrimSpace(p)
			continue
		}
		m := benchLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := pkg + " " + m[1]
		b, ok := byKey[key]
		if !ok {
			b = &goBench{Pkg: pkg, Name: m[1]}
			byKey[key] = b
			order = append(order, key)
		}
		ns, _ := strconv.ParseFloat(m[3], 64)
		b.NsOp = append(b.NsOp, ns)
		if m[4] != "" {
			b.BytesOp, _ = strconv.ParseFloat(m[4], 64)
		}
		if m[5] != "" {
			b.AllocsOp, _ = strconv.ParseFloat(m[5], 64)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	res := goBenchResult{Kind: "gobench"}
	sort.Strings(order)
	for _, k := range order {
		b := byKey[k]
		b.Ns = summarize(b.NsOp)
		res.Benches = append(res.Benches, *b)
	}
	if len(res.Benches) == 0 {
		return errors.New("no benchmark results in " + *in)
	}
	return writeJSON(*out, res)
}
