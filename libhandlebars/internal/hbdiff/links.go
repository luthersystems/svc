// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbdiff

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LinkCheck vets the resolved target of a symlink found among a harness
// input's entries before the harness follows it: a non-nil error refuses
// the input. hbdiff refuses targets inside an svc work tree with it.
type LinkCheck func(resolved string) error

// FollowEntry returns the file info of entry e of dir, following a symlink
// after check (nil: none) has accepted its target. A dangling link is an
// error: no input is silently dropped.
func FollowEntry(dir string, e fs.DirEntry, check LinkCheck) (fs.FileInfo, error) {
	p := filepath.Join(dir, e.Name())
	if e.Type()&fs.ModeSymlink == 0 {
		return e.Info()
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(r); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return os.Stat(r)
}
