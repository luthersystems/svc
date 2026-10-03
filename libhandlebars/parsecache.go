// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package libhandlebars

import (
	"strings"
	"sync"

	"github.com/luthersystems/raymond"
)

// Bounds on the must-parse verdict cache. Templates are phylum literals in
// practice, so a few MiB covers every real phylum; once the cache is full,
// further templates are parsed on every call exactly as before. Entries are
// never evicted, so the first templates a process validates keep their place.
const (
	mustParseCacheMaxTemplate = 64 << 10
	mustParseCacheMaxBytes    = 4 << 20
	// mustParseEntryOverhead approximates the map bucket and string headers
	// of one entry, so a cache of short templates stays near its byte bound.
	mustParseEntryOverhead = 96
)

// mustParseVerdict is the outcome of raymond.Parse on one template: nothing
// but immutable strings, so one process-wide cache can serve every VM.
type mustParseVerdict struct {
	errMsg string
	failed bool
}

var mustParseCache struct {
	mu    sync.RWMutex
	m     map[string]mustParseVerdict
	bytes int
}

// mustParseVerdictOf returns the verdict of raymond.Parse on tpl, cached per
// template string. The verdict depends only on tpl, so a cache hit and a miss
// give the same result. Phyla and test files validate the same literal
// templates on every load (substrate#548), which re-parsed each one.
func mustParseVerdictOf(tpl string) mustParseVerdict {
	mustParseCache.mu.RLock()
	v, ok := mustParseCache.m[tpl]
	mustParseCache.mu.RUnlock()
	if ok {
		return v
	}
	if _, err := raymond.Parse(tpl); err != nil {
		v = mustParseVerdict{errMsg: err.Error(), failed: true}
	}
	if len(tpl) > mustParseCacheMaxTemplate {
		return v
	}
	mustParseCache.mu.Lock()
	defer mustParseCache.mu.Unlock()
	if _, ok := mustParseCache.m[tpl]; ok {
		return v
	}
	cost := len(tpl) + len(v.errMsg) + mustParseEntryOverhead
	if mustParseCache.bytes+cost > mustParseCacheMaxBytes {
		return v
	}
	if mustParseCache.m == nil {
		mustParseCache.m = make(map[string]mustParseVerdict)
	}
	// Clone: do not pin a larger buffer the template string may slice.
	key := strings.Clone(tpl)
	mustParseCache.m[key] = v
	mustParseCache.bytes += cost
	return v
}
