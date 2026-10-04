// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A corpus file is a txtar archive (the golang.org/x/tools/txtar format,
// parsed here to avoid the dependency):
//
//	free comment text
//	-- name.hbs --
//	template
//	-- name.json --
//	{"one": "context"}
//	-- other.hbs --
//	...
//	-- other.jsonl --
//	{"one": "context per line"}
//	{"another": 1}
//
// Each name.hbs is a template. Exactly one trailing newline is removed
// from it, so a template that must end in a newline is followed by a blank
// line. Its contexts are name.json (taken whole, and may be invalid JSON
// on purpose), or name.jsonl (one per non-blank line), or, when it has
// neither, the file's _.jsonl, or else {}. Case names are
// <file>/<name>, with #<line> appended for jsonl contexts.

// TxtarFile is one section of a txtar archive.
type TxtarFile struct {
	Name string
	Data []byte
}

// ParseTxtar parses txtar data. It returns the leading comment and the
// files in order.
func ParseTxtar(data []byte) ([]byte, []TxtarFile) {
	comment, name, data := findMarker(data)
	var files []TxtarFile
	for name != "" {
		f := TxtarFile{Name: name}
		var next string
		f.Data, next, data = findMarker(data)
		files = append(files, f)
		name = next
	}
	return comment, files
}

// findMarker finds the next "-- name --" line. It returns the data before
// it, the name, and the data after the marker line.
func findMarker(data []byte) ([]byte, string, []byte) {
	var i int
	for {
		if name, after := isMarker(data[i:]); name != "" {
			return data[:i], name, after
		}
		j := bytes.IndexByte(data[i:], '\n')
		if j < 0 {
			return fixNL(data), "", nil
		}
		i += j + 1
	}
}

func isMarker(data []byte) (string, []byte) {
	if !bytes.HasPrefix(data, []byte("-- ")) {
		return "", nil
	}
	line, after, _ := bytes.Cut(data, []byte("\n"))
	line = bytes.TrimRight(line, "\r")
	if !bytes.HasSuffix(line, []byte(" --")) || len(line) < 7 {
		return "", nil
	}
	return strings.TrimSpace(string(line[3 : len(line)-3])), after
}

func fixNL(data []byte) []byte {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data
	}
	return append(append([]byte{}, data...), '\n')
}

// FormatTxtar is the inverse of ParseTxtar.
func FormatTxtar(comment []byte, files []TxtarFile) []byte {
	var b bytes.Buffer
	b.Write(fixNL(comment))
	for _, f := range files {
		fmt.Fprintf(&b, "-- %s --\n", f.Name)
		b.Write(fixNL(f.Data))
	}
	return b.Bytes()
}

// LoadCorpusFile reads one corpus txtar file into cases.
func LoadCorpusFile(file string) ([]Case, error) {
	data, err := os.ReadFile(file) //nolint:gosec // corpus path chosen by the harness
	if err != nil {
		return nil, err
	}
	group := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	return CorpusCases(group, data)
}

// CorpusCases expands txtar data into cases named group/<name>.
func CorpusCases(group string, data []byte) ([]Case, error) {
	_, files := ParseTxtar(data)
	byName := map[string][]byte{}
	var tpls []string
	for _, f := range files {
		if _, dup := byName[f.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate section %s", group, f.Name)
		}
		byName[f.Name] = f.Data
		if strings.HasSuffix(f.Name, ".hbs") {
			tpls = append(tpls, strings.TrimSuffix(f.Name, ".hbs"))
		}
	}
	for name := range byName {
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".jsonl")
		if base == name && !strings.HasSuffix(name, ".hbs") {
			return nil, fmt.Errorf("%s: section %s is not .hbs, .json or .jsonl", group, name)
		}
		if base != name && base != "_" {
			if _, ok := byName[base+".hbs"]; !ok {
				return nil, fmt.Errorf("%s: context %s has no template", group, name)
			}
		}
	}
	var cases []Case
	for _, t := range tpls {
		tpl := string(byName[t+".hbs"])
		tpl = strings.TrimSuffix(tpl, "\n")
		name := group + "/" + t
		if ctx, ok := byName[t+".json"]; ok {
			cases = append(cases, Case{Name: name, Template: tpl, Context: bytes.TrimRight(ctx, "\n")})
			continue
		}
		lines, ok := byName[t+".jsonl"]
		if !ok {
			lines, ok = byName["_.jsonl"]
		}
		if !ok {
			cases = append(cases, Case{Name: name, Template: tpl, Context: []byte("{}")})
			continue
		}
		for i, l := range bytes.Split(lines, []byte("\n")) {
			if len(bytes.TrimSpace(l)) == 0 {
				continue
			}
			cases = append(cases, Case{Name: fmt.Sprintf("%s#%d", name, i+1), Template: tpl, Context: l})
		}
	}
	return cases, nil
}

// LoadCorpusDir loads every *.txtar file in dir, sorted by name.
func LoadCorpusDir(dir string) (map[string][]Case, []string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.txtar"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	out := map[string][]Case{}
	var groups []string
	for _, f := range files {
		cs, err := LoadCorpusFile(f)
		if err != nil {
			return nil, nil, err
		}
		g := strings.TrimSuffix(filepath.Base(f), ".txtar")
		out[g] = cs
		groups = append(groups, g)
	}
	return out, groups, nil
}
