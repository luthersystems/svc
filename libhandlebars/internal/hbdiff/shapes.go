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
func LoadShapes(dir string) ([]Case, error) {
	tpls, err := filepath.Glob(filepath.Join(dir, "*", "template.hbs"))
	if err != nil {
		return nil, err
	}
	sort.Strings(tpls)
	var cases []Case
	for _, tf := range tpls {
		tpl, err := os.ReadFile(tf) //nolint:gosec // harness testdata path
		if err != nil {
			return nil, err
		}
		name := filepath.Base(filepath.Dir(tf))
		ctxs, err := filepath.Glob(filepath.Join(filepath.Dir(tf), "ctx", "*.json"))
		if err != nil {
			return nil, err
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
