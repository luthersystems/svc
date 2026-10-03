// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/luthersystems/elps/parser/lexer"
	"github.com/luthersystems/elps/parser/token"
)

// Template is a template found on disk.
type Template struct {
	// Name says where it came from: a file path, or path:line for a
	// string literal in a .lisp file.
	Name   string
	Source string
	// Lisp is true for a template found as an ELPS string literal.
	Lisp bool
}

// ExtractPhylum finds the templates in a phylum directory: every .html
// file, and every ELPS string literal containing "{{" in a non-test .lisp
// file. Literals are taken whether or not they reach handlebars:render
// directly, so templates stored in data (labels in a table, say) are
// included. Duplicates by content are dropped, keeping the first.
func ExtractPhylum(dir string) ([]Template, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(p, ".html"):
			files = append(files, p)
		case strings.HasSuffix(p, ".lisp") && !strings.HasSuffix(p, "_test.lisp"):
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []Template
	seen := map[string]bool{}
	add := func(t Template) {
		// A generated .lisp copy of an .html template differs by a
		// trailing newline.
		key := strings.TrimRight(t.Source, "\n")
		if !seen[key] {
			seen[key] = true
			out = append(out, t)
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // the caller names the phylum directory
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(dir, f)
		if strings.HasSuffix(f, ".html") {
			add(Template{Name: rel, Source: string(b)})
			continue
		}
		lits, err := LispTemplateLiterals(rel, string(b))
		if err != nil {
			return nil, err
		}
		for _, t := range lits {
			add(t)
		}
	}
	return out, nil
}

// LispTemplateLiterals returns the string literals in ELPS source that
// contain "{{", tokenized by the ELPS lexer.
func LispTemplateLiterals(file, src string) ([]Template, error) {
	lex := lexer.New(token.NewScannerString(file, src))
	var out []Template
	for {
		toks := lex.ReadToken()
		for _, tok := range toks {
			var s string
			switch tok.Type {
			case token.EOF:
				return out, nil
			case token.ERROR:
				return nil, fmt.Errorf("%s: %s", file, tok.Text)
			case token.STRING:
				u, err := strconv.Unquote(tok.Text)
				if err != nil {
					continue
				}
				s = u
			case token.STRING_RAW:
				if len(tok.Text) < 6 {
					continue
				}
				s = tok.Text[3 : len(tok.Text)-3]
			default:
				continue
			}
			if strings.Contains(s, "{{") {
				out = append(out, Template{Name: fmt.Sprintf("%s:%d", file, tok.Source.Line), Source: s, Lisp: true})
			}
		}
	}
}
