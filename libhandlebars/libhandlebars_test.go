package libhandlebars_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/elps/elpstest"
	"github.com/luthersystems/elps/elpsutil"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/luthersystems/elps/lisp/lisplib/libtesting"
	"github.com/luthersystems/svc/libhandlebars"
)

// TestPackage runs libhandlebars lisp tests.
func TestPackage(t *testing.T) {
	runner := &elpstest.Runner{
		LoaderFn: elpsutil.LoadAll(libtesting.LoadPackage, libjson.LoadPackage, libhandlebars.LoadPackage),
	}
	runner.RunTestFile(t, "libhandlebars_test.lisp")
}

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		tplStr string
		err    bool
	}{
		{
			name:   "test parse (ok)",
			tplStr: `{{value}}`,
		},
		{
			name:   "test parse (bad)",
			tplStr: `{{{value}}`,
			err:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := libhandlebars.Parse(test.tplStr)
			if test.err != true {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestRenderGoValue(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{#each xs}}{{this}},{{/each}}{{n}}`)
	require.NoError(t, err)
	res, err := libhandlebars.Render(tpl, struct {
		Xs []int `json:"xs"`
		N  int   `json:"n"`
	}{Xs: []int{1, 2}, N: 3})
	require.NoError(t, err)
	require.Equal(t, "1,2,3", res)
}

// TestRenderGoValueTypes pins that Render converts Go values through JSON:
// an int becomes a float64, so includeZero (literal 0 only in compat mode)
// no longer matches it, as it did under raymond.
func TestRenderGoValueTypes(t *testing.T) {
	tpl, err := libhandlebars.Parse(`{{#if n includeZero=true}}yes{{else}}no{{/if}} {{to-str n}}`)
	require.NoError(t, err)
	res, err := libhandlebars.Render(tpl, map[string]any{"n": 0})
	require.NoError(t, err)
	require.Equal(t, "no 0.000000", res)
}

func TestRender(t *testing.T) {
	tplStr := `{{value}}`
	tpl, err := libhandlebars.Parse(tplStr)
	require.NoError(t, err)
	ctx := map[string]string{
		"value": "Some value",
	}

	res, err := libhandlebars.Render(tpl, ctx)
	require.NoError(t, err)
	require.Equal(t, "Some value", res)
}

func TestRenderWithHelper(t *testing.T) {
	tplStr := `{{#if (eq val1 val2)}}eq works{{/if}}`
	tpl, err := libhandlebars.Parse(tplStr)
	require.NoError(t, err)
	ctx := map[string]string{
		"val1": "1",
		"val2": "1",
	}

	expected := "eq works"

	res, err := libhandlebars.Render(tpl, ctx)
	require.NoError(t, err)
	require.Equal(t, expected, res)
}
