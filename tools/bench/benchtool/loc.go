package main

import (
	"bufio"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type locCount struct {
	Files    int `json:"files"`
	Lines    int `json:"lines"`
	NonBlank int `json:"non_blank"`
}

type locResult struct {
	Kind  string               `json:"kind"`
	Count map[string]*locCount `json:"count"`
	// Go test functions by kind (Test, Benchmark, Fuzz, Example), top level only.
	GoFuncs map[string]int `json:"go_test_funcs"`
}

var goTestFunc = regexp.MustCompile(`^func (Test|Benchmark|Fuzz|Example)[A-Z_0-9]?\w*\(`)

// loc counts lines by kind: Go (hand written, generated, tests), the web
// apps' TypeScript and Vue, the SQL migrations, docs and shell scripts.
func loc(args []string) error {
	fs := flag.NewFlagSet("loc", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	out := fs.String("out", "", "JSON result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("usage: loc -root . -out F.json")
	}
	res := locResult{Kind: "loc", Count: map[string]*locCount{}, GoFuncs: map[string]int{}}
	skip := map[string]bool{".git": true, "node_modules": true, "dist": true, "bin": true, ".claude": true}
	err := filepath.WalkDir(*root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(*root, p)
		if d.IsDir() {
			if skip[d.Name()] || rel == filepath.Join("docs", "bench") {
				return filepath.SkipDir
			}
			return nil
		}
		kind := locKind(filepath.ToSlash(rel))
		if kind == "" {
			return nil
		}
		c, ok := res.Count[kind]
		if !ok {
			c = &locCount{}
			res.Count[kind] = c
		}
		return countFile(p, c, kind == "go_test", res.GoFuncs)
	})
	if err != nil {
		return err
	}
	return writeJSON(*out, res)
}

func locKind(rel string) string {
	ext := filepath.Ext(rel)
	switch {
	case ext == ".go" && strings.HasPrefix(rel, "gen/"):
		return "go_generated"
	case strings.HasSuffix(rel, "_test.go"):
		return "go_test"
	case ext == ".go":
		return "go"
	case strings.HasPrefix(rel, "web/") && (ext == ".ts" || ext == ".tsx"):
		if strings.Contains(rel, ".spec.") || strings.Contains(rel, ".test.") || strings.Contains(rel, "/tests/") {
			return "ts_test"
		}
		return "ts"
	case strings.HasPrefix(rel, "web/") && ext == ".vue":
		return "vue"
	case strings.HasPrefix(rel, "internal/migrate/migrations/") && ext == ".sql":
		return "sql_migrations"
	case ext == ".md" && strings.HasPrefix(rel, "docs/wiki/"):
		return "docs_wiki"
	case ext == ".md":
		return "docs"
	case ext == ".sh":
		return "shell"
	case ext == ".proto":
		return "proto"
	}
	return ""
}

func countFile(p string, c *locCount, tests bool, funcs map[string]int) error {
	f, err := os.Open(p) //nolint:gosec // a file of this repository
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // read only
	c.Files++
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		c.Lines++
		if strings.TrimSpace(line) != "" {
			c.NonBlank++
		}
		if tests {
			if m := goTestFunc.FindStringSubmatch(line); m != nil {
				funcs[m[1]]++
			}
		}
	}
	return sc.Err()
}
