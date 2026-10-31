package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fileSize struct {
	Path string `json:"path"`
	Raw  int64  `json:"raw"`
	Gzip int64  `json:"gzip,omitempty"`
}

type sizeSet struct {
	Label     string     `json:"label"`
	Files     []fileSize `json:"files"`
	TotalRaw  int64      `json:"total_raw"`
	TotalGzip int64      `json:"total_gzip,omitempty"`
}

// sizes measures files (directories are walked): raw bytes and, with
// -gzip, gzip -9 bytes as Vite's build report counts them.
func sizes(args []string) error {
	fs := flag.NewFlagSet("sizes", flag.ContinueOnError)
	out := fs.String("out", "", "JSON file to write")
	label := fs.String("label", "", "what is measured")
	gz := fs.Bool("gzip", false, "also measure gzip -9 sizes")
	strip := fs.String("strip-prefix", "", "prefix to cut from the paths in the output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() == 0 {
		return errors.New("usage: sizes -out F.json -label L [-gzip] PATH [PATH]")
	}
	set := sizeSet{Label: *label}
	for _, root := range fs.Args() {
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			f, err := measure(p, *gz)
			if err != nil {
				return err
			}
			f.Path = strings.TrimPrefix(p, *strip)
			set.Files = append(set.Files, f)
			return nil
		})
		if err != nil {
			return err
		}
	}
	sort.Slice(set.Files, func(i, j int) bool { return set.Files[i].Raw > set.Files[j].Raw })
	for _, f := range set.Files {
		set.TotalRaw += f.Raw
		set.TotalGzip += f.Gzip
	}
	return writeJSON(*out, set)
}

func measure(p string, gz bool) (fileSize, error) {
	b, err := os.ReadFile(p) //nolint:gosec // a build output named by the caller
	if err != nil {
		return fileSize{}, err
	}
	f := fileSize{Raw: int64(len(b))}
	if gz {
		var buf bytes.Buffer
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := w.Write(b); err != nil {
			return f, err
		}
		if err := w.Close(); err != nil {
			return f, err
		}
		f.Gzip = int64(buf.Len())
	}
	return f, nil
}
