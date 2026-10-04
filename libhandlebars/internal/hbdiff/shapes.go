// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadShapes reads a shape corpus: <dir>/<name>/template.hbs with its
// contexts in <dir>/<name>/ctx/*.json. Cases are named shapes/<name>#<ctx>.
// Symlinks (case directories, templates, ctx directories, contexts) are
// followed once check (nil: none) accepts their target; a dangling link is
// an error.
func LoadShapes(dir string, check LinkCheck) ([]Case, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var tpls []string
	for _, e := range ents {
		info, err := FollowEntry(dir, e, check)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		caseDir := filepath.Join(dir, e.Name())
		sub, err := os.ReadDir(caseDir)
		if err != nil {
			return nil, err
		}
		for _, s := range sub {
			if s.Name() != "template.hbs" && s.Name() != "ctx" {
				continue
			}
			if _, err := FollowEntry(caseDir, s, check); err != nil {
				return nil, err
			}
			if s.Name() == "template.hbs" {
				tpls = append(tpls, filepath.Join(caseDir, s.Name()))
			}
		}
	}
	sort.Strings(tpls)
	var cases []Case
	for _, tf := range tpls {
		tpl, err := os.ReadFile(tf) //nolint:gosec // harness input, links vetted above
		if err != nil {
			return nil, err
		}
		name := filepath.Base(filepath.Dir(tf))
		ctxDir := filepath.Join(filepath.Dir(tf), "ctx")
		var ctxs []string
		if cents, err := os.ReadDir(ctxDir); err == nil {
			for _, c := range cents {
				if !strings.HasSuffix(c.Name(), ".json") {
					continue
				}
				info, err := FollowEntry(ctxDir, c, check)
				if err != nil {
					return nil, err
				}
				if !info.IsDir() {
					ctxs = append(ctxs, filepath.Join(ctxDir, c.Name()))
				}
			}
		}
		if len(ctxs) == 0 {
			return nil, fmt.Errorf("%s: no contexts", name)
		}
		sort.Strings(ctxs)
		for _, cf := range ctxs {
			ctx, err := os.ReadFile(cf) //nolint:gosec // harness testdata path
			if err != nil {
				return nil, err
			}
			cases = append(cases, Case{
				Name:     "shapes/" + name + "#" + strings.TrimSuffix(filepath.Base(cf), ".json"),
				Template: string(tpl),
				Context:  ctx,
			})
		}
	}
	return cases, nil
}
