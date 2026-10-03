// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func TestJSONGoContextSetting(t *testing.T) {
	for _, c := range []struct {
		v     string
		ok    bool
		want  bool
		level logrus.Level // 0: nothing logged
	}{
		{"", false, false, 0},
		{"", true, false, 0},
		{"true", true, true, logrus.InfoLevel},
		{"TRUE", true, false, logrus.WarnLevel},
		{"1", true, false, logrus.WarnLevel},
		{" true", true, false, logrus.WarnLevel},
		{"false", true, false, logrus.WarnLevel},
	} {
		log, hook := test.NewNullLogger()
		require.Equal(t, c.want, jsonGoContextSetting(c.v, c.ok, log), "%q", c.v)
		if c.level == 0 {
			require.Empty(t, hook.AllEntries(), "%q", c.v)
			continue
		}
		require.Len(t, hook.AllEntries(), 1, "%q", c.v)
		require.Equal(t, c.level, hook.LastEntry().Level, "%q", c.v)
	}
}

// TestJSONStringLen: jsonStringLen is the length encoding/json (whose
// escaping libjson copies) writes, for every byte and the escaped runes.
func TestJSONStringLen(t *testing.T) {
	ins := []string{"", "plain", "a\"b\\c", "\b\f\n\r\t", "<>&", "  ", "é漢🙂", "\xff\xfe", "\xe2\x80", "x\x00\x1fy"}
	for b := range 256 {
		ins = append(ins, string([]byte{byte(b)}), "a"+string([]byte{byte(b)})+"é")
	}
	for _, s := range ins {
		want, err := json.Marshal(s)
		require.NoError(t, err)
		require.Equal(t, int64(len(want)), jsonStringLen(s), "%q", s)
	}
}
