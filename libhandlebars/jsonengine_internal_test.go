// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// jsonV2 reports whether encoding/json runs on Go 1.27's v2 engine, the
// default (GOEXPERIMENT=nojsonv2 selects v1). jsongo.go models the v1
// engine: its lengths for invalid UTF-8 and some error texts differ from
// v2's (luthersystems/svc#147). The tests that use encoding/json as the
// reference do so only on v1; CI runs them with GOEXPERIMENT=nojsonv2.
var jsonV2 = func() bool {
	b, err := json.Marshal("\xff")
	return err == nil && len(b) == len(`"`+"\ufffd"+`"`)
}()

// requireMarshalError requires got to fail as json.Marshal failed with
// want. On the v1 engine the texts must be equal; on v2 they can differ
// (#147), so only both must be errors.
func requireMarshalError(t *testing.T, want, got error, msgAndArgs ...any) {
	t.Helper()
	require.Error(t, want, msgAndArgs...)
	if jsonV2 {
		require.Error(t, got, msgAndArgs...)
		return
	}
	require.EqualError(t, got, want.Error(), msgAndArgs...)
}

func TestJSONEngineDetected(t *testing.T) {
	b, err := json.Marshal("\xff")
	require.NoError(t, err)
	if jsonV2 {
		require.Equal(t, `"`+"\ufffd"+`"`, string(b))
	} else {
		require.Equal(t, `"\ufffd"`, string(b))
	}
}

// JSONV2EngineForTest reports jsonV2 to the external tests.
func JSONV2EngineForTest() bool { return jsonV2 }
