// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConfigParseCacheBounds: with the default cache bounds a loader
// shares the process-wide cache; with others it has its own, which honours
// them: no template longer than ParseCacheMaxEntryBytes is kept, and
// entries past ParseCacheMaxBytes are evicted.
func TestConfigParseCacheBounds(t *testing.T) {
	def, err := buildConfig(nil)
	require.NoError(t, err)
	require.Nil(t, newParser(def).cache)

	cfg, err := buildConfig([]Option{WithParseCache(1<<20, 10)})
	require.NoError(t, err)
	ps := newParser(cfg)
	require.NotNil(t, ps.cache)
	_, err = ps.parse("{{a}} long enough", cfg.Limits, nil)
	require.NoError(t, err)
	n, _ := ps.cache.Stats()
	require.Zero(t, n, "over the entry bound")
	_, err = ps.parse("{{a}}", cfg.Limits, nil)
	require.NoError(t, err)
	n, _ = ps.cache.Stats()
	require.Equal(t, 1, n)

	cfg, err = buildConfig([]Option{WithParseCache(700, 1<<20)})
	require.NoError(t, err)
	ps = newParser(cfg)
	for _, src := range []string{"{{a}}", "{{b}}", "{{c}}"} {
		_, err = ps.parse(src, cfg.Limits, nil)
		require.NoError(t, err)
	}
	n, bytes := ps.cache.Stats()
	require.Less(t, n, 3, "evicted past the byte bound")
	require.LessOrEqual(t, bytes, 700)
}
