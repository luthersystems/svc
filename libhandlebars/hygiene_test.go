// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBuildArtefactsTracked fails if the repository tracks a compiled
// binary (ELF or Mach-O) or a test binary or profile: they are build
// output, and svc is public.
func TestNoBuildArtefactsTracked(t *testing.T) {
	root, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git work tree")
	}
	files, err := exec.CommandContext(t.Context(), "git", "-C", strings.TrimSpace(string(root)), "ls-files", "-z").Output() //nolint:gosec // git on this repository's own root
	if err != nil {
		t.Skip("git ls-files failed")
	}
	magics := [][]byte{
		[]byte("\x7fELF"),
		{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe}, // Mach-O 32-bit
		{0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64-bit
		{0xca, 0xfe, 0xba, 0xbe}, // Mach-O universal
	}
	for _, name := range strings.Split(strings.TrimRight(string(files), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		for _, ext := range []string{".test", ".prof", ".pprof"} {
			if strings.HasSuffix(name, ext) {
				t.Errorf("%s is tracked: build output", name)
			}
		}
		f, err := os.Open(filepath.Join(strings.TrimSpace(string(root)), name)) //nolint:gosec // a tracked file of this repository
		if err != nil {
			continue // deleted in the work tree
		}
		head := make([]byte, 4)
		n, _ := f.Read(head)
		_ = f.Close()
		for _, m := range magics {
			if n == 4 && bytes.Equal(head, m) {
				t.Errorf("%s is tracked: a compiled binary", name)
			}
		}
	}
}
