// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
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
