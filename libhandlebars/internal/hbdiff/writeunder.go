// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteUnder writes data to rel inside root, creating directories as
// needed. It refuses any rel that is absolute or escapes root, and any
// path that resolves through a symlink to outside root, so tools that
// handle private templates never write elsewhere.
func WriteUnder(root, rel string, data []byte) error {
	if filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
		return fmt.Errorf("refusing to write %q outside %s", rel, root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if dir := filepath.Dir(rel); dir != "." {
		if err := r.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return r.WriteFile(rel, data, 0o600)
}

// SafeName turns an arbitrary label into one path element.
func SafeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
	s = strings.Trim(s, ".")
	if s == "" {
		s = "_"
	}
	return s
}
