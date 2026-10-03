// Copyright © 2026 Luther Systems, Ltd. All right reserved.

// Command hbshape turns private handlebars templates into anonymous
// skeletons with generated contexts, for the checked-in shape corpus of
// the differential harness (libhandlebars/internal/hbdiff).
//
//	hbshape -phylum <dir> -out <dir> -salt-file <file> [-contexts 8] [-seed 1]
//	hbshape -out <dir> -salt-file <file> template.hbs ...
//
// Skeletons keep block structure, else chains, ../ depth, stash kinds,
// ~ and standalone whitespace, helper names and helper-significant hash
// keys. Content becomes x/0 filler; identifiers and string literals become
// HMAC names under the salt, which must stay private. Before writing
// anything it checks every output against every word (4+ characters) of
// the sources and refuses to write if one appears.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luthersystems/svc/libhandlebars/internal/hbdiff"
	"github.com/luthersystems/svc/libhandlebars/internal/hbdiff/shape"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hbshape:", err)
		os.Exit(1)
	}
}

type output struct {
	name     string
	skeleton string
	contexts [][]byte
}

func run() error {
	phylum := flag.String("phylum", "", "phylum directory: .html files and ELPS string literals containing {{")
	out := flag.String("out", "", "output directory (required); nothing is written outside it")
	saltFile := flag.String("salt-file", "", "file holding the private HMAC salt (required)")
	nctx := flag.Int("contexts", 8, "contexts to generate per template")
	seed := flag.Uint64("seed", 1, "context generator seed")
	showLeaks := flag.Bool("show-leaks", false, "print leaked source words to stderr (they are private)")
	flag.Parse()
	if *out == "" || *saltFile == "" {
		return errors.New("-out and -salt-file are required")
	}
	salt, err := os.ReadFile(*saltFile)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(salt))) < 16 {
		return errors.New("salt must be at least 16 characters")
	}

	var tpls []hbdiff.Template
	if *phylum != "" {
		found, err := hbdiff.ExtractPhylum(*phylum, nil)
		if err != nil {
			return err
		}
		tpls = append(tpls, found...)
	}
	for _, f := range flag.Args() {
		b, err := os.ReadFile(f) //nolint:gosec // operator-named input
		if err != nil {
			return err
		}
		tpls = append(tpls, hbdiff.Template{Name: f, Source: string(b)})
	}
	if len(tpls) == 0 {
		return errors.New("no templates")
	}

	an := shape.NewAnonymizer(salt)
	var outs []output
	var tokens []string
	nDoc, nLabel := 0, 0
	for _, t := range tpls {
		skel, err := an.Skeleton(t.Source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", t.Name, err)
			continue
		}
		var name string
		if t.Lisp && len(t.Source) < 4096 {
			nLabel++
			name = fmt.Sprintf("label-%02d", nLabel)
		} else {
			nDoc++
			name = fmt.Sprintf("doc-%02d", nDoc)
		}
		fmt.Fprintf(os.Stderr, "%s <- %s\n", name, t.Name)
		tokens = append(tokens, shape.SourceTokens(t.Source)...)
		outs = append(outs, output{name: name, skeleton: skel})
	}
	for i := range outs {
		schema, err := shape.Infer(outs[i].skeleton)
		if err != nil {
			return fmt.Errorf("%s: skeleton does not parse: %w", outs[i].name, err)
		}
		outs[i].contexts = shape.Generate(schema, *nctx, *seed, an.Strings)
	}

	// Verify everything before writing anything.
	var all []string
	for _, o := range outs {
		if err := shape.CheckSkeleton(o.skeleton); err != nil {
			return fmt.Errorf("%s: %w", o.name, err)
		}
		all = append(all, o.skeleton)
		for _, c := range o.contexts {
			if err := shape.CheckContext(c); err != nil {
				return fmt.Errorf("%s: %w", o.name, err)
			}
			all = append(all, string(c))
		}
	}
	if leaks := shape.Leaks(tokens, all...); len(leaks) > 0 {
		if *showLeaks {
			fmt.Fprintln(os.Stderr, strings.Join(leaks, "\n"))
		}
		return fmt.Errorf("refusing to write: %d source words appear in the output (not printed)", len(leaks))
	}

	for _, o := range outs {
		if err := hbdiff.WriteUnder(*out, filepath.Join(o.name, "template.hbs"), []byte(o.skeleton)); err != nil {
			return err
		}
		for i, c := range o.contexts {
			if err := hbdiff.WriteUnder(*out, filepath.Join(o.name, "ctx", fmt.Sprintf("%02d.json", i)), append(c, '\n')); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %d skeletons, %d contexts each, under %s\n", len(outs), *nctx, *out)
	return nil
}
