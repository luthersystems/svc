package hbdiff

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/svc/libhandlebars/internal/hbdiff/shape"
)

const shapesDir = "testdata/shapes"

func loadShapes(t testing.TB) []Case {
	t.Helper()
	cases, err := LoadShapes(shapesDir)
	require.NoError(t, err)
	require.NotEmpty(t, cases)
	return cases
}

// TestShapesAnonymous asserts the checked-in skeletons and contexts carry
// only hash names, kept helper names and keywords, filler content and
// generated values. The check that no source word appears ran in hbshape
// before the files were written; this keeps later edits honest.
func TestShapesAnonymous(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range loadShapes(t) {
		if !seen[c.Template] {
			seen[c.Template] = true
			require.NoError(t, shape.CheckSkeleton(c.Template), c.Name)
		}
		require.NoError(t, shape.CheckContext(c.Context), c.Name)
	}
}

// TestShapesGolden pins the reference output of every shape case by hash.
func TestShapesGolden(t *testing.T) {
	file := filepath.Join(goldenDir, "shapes.json")
	want, err := ReadGoldens(file)
	require.NoError(t, err)
	got := map[string]Golden{}
	n := 2
	if *update {
		n = 20
	}
	for _, c := range loadShapes(t) {
		ref, alt := repeat(n, func() Result { return Ref(c.Template, c.Context) })
		got[c.Name] = GoldenOf(ref, alt, true)
		if *update {
			continue
		}
		w, ok := want[c.Name]
		if !ok {
			t.Errorf("%s: no golden; run with -update", c.Name)
			continue
		}
		if len(alt) > 0 && !w.Nondet {
			t.Errorf("%s: reference is nondeterministic but the golden is not", c.Name)
			continue
		}
		if !w.Matches(ref) {
			t.Errorf("%s: reference drifted from golden: got %s", c.Name, describe(ref))
		}
	}
	if *update {
		require.NoError(t, WriteGoldens(file, got))
	}
}

// TestShapesDiff compares the candidate with the reference on the shape
// corpus.
func TestShapesDiff(t *testing.T) {
	rep := Run(loadShapes(t), Options{Runs: *runs, Candidate: DefaultCandidate, Allow: loadAllowlist(t)})
	reportT(t, rep)
}
