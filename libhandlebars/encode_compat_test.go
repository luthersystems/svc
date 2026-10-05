// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars_test

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/lisp/lisplib/libjson"
	"github.com/stretchr/testify/require"
)

// TestEncodeNativeLoadErrorParity follows json:dump-bytes when a native
// can fail its load check or allocation cap, in both render modes.
func TestEncodeNativeLoadErrorParity(t *testing.T) {
	digits := "1" + strings.Repeat("0", 400)
	n, ok := new(big.Int).SetString(digits, 10)
	require.True(t, ok)
	var deep any = 1
	for range 10_001 {
		deep = []any{deep}
	}
	for _, c := range []struct {
		name   string
		native any
	}{
		{"raw number", struct {
			S string
			R json.RawMessage
		}{strings.Repeat("s", 4096), json.RawMessage("1e1000")}},
		{"big integer", struct {
			S string
			N *big.Int
		}{strings.Repeat("s", 2048), n}},
		{"deep native", deep},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, fn := range []string{"render", strictMode} {
				env := newEnv(t)
				env.Runtime.MaxAlloc = 1024
				ctx := sortedMap("n", lisp.Native(c.native))
				dump := libjson.DefaultSerializer().DumpBytesBuiltin(env,
					lisp.SExpr([]*lisp.LVal{ctx, lisp.Bool(false)}))
				require.Equal(t, lisp.LError, dump.Type)
				res, _ := renderIn(t, env, fn, "", ctx)
				require.Equal(t, lisp.LError, res.Type)
				require.Contains(t, res.Cells[0].Str, dump.Cells[0].Str)
			}
		})
	}
}

// TestEncodeSharedNativeOnce covers the same native reached through several
// containers: libjson's memo must reuse its bytes along every path rather
// than call the original marshaler again on a later path.
func TestEncodeSharedNativeOnce(t *testing.T) {
	for _, fn := range []string{"render", strictMode} {
		env := newEnv(t)
		calls := 0
		n := lisp.Native(onceMarshaler{calls: &calls})
		ctx := sortedMap("a", n, "b", lisp.Array(nil, []*lisp.LVal{n}), "c", sortedMap("n", n))
		res, _ := renderIn(t, env, fn, "{{a.x}}|{{b.[0].x}}|{{c.n.x}}", ctx)
		require.Equal(t, lisp.LString, res.Type, "%.300v", res)
		require.Equal(t, "once|once|once", res.Str)
		require.Equal(t, 1, calls)
	}
}
