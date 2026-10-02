package hbs

import (
	"container/list"
	"crypto/sha256"
	"sync"
)

// Parse cache bounds. A cached entry is charged its source length plus
// cacheEntryOverhead bytes; an entry whose source is over
// cacheMaxEntryBytes is never cached.
const (
	cacheMaxBytes      = 8 << 20
	cacheMaxEntryBytes = 1 << 20
	cacheEntryOverhead = 256
)

// cacheKey identifies a parse: the source's SHA-256 and the limits Parse
// applied, since the limits decide the verdict too.
type cacheKey struct {
	sum      [sha256.Size]byte
	maxBytes int
	maxDepth int
}

// cacheEntry is a cached verdict: a program, or the error Parse returned.
type cacheEntry struct {
	prog *Program
	err  *Error
	key  cacheKey
	cost int
}

// parseCache is a byte-bounded LRU of parse verdicts.
type parseCache struct {
	entries  map[cacheKey]*list.Element
	lru      list.List // of *cacheEntry, most recent first
	bytes    int
	maxBytes int
	mu       sync.Mutex
}

var defaultCache = newParseCache(cacheMaxBytes)

func newParseCache(maxBytes int) *parseCache {
	return &parseCache{entries: make(map[cacheKey]*list.Element), maxBytes: maxBytes}
}

// ParseCached is Parse with a process-wide cache of verdicts, programs and
// errors alike, keyed by the SHA-256 of src and the effective limits. It
// returns exactly what Parse returns for the same arguments: a Program is
// immutable, so sharing one is safe, and each call gets its own copy of a
// cached *Error. The cache holds at most 8 MiB of template source and
// does not keep templates over 1 MiB.
//
// Callers that meter work must charge for a parse on a hit as on a miss,
// so the cache cannot change what a transaction costs.
func ParseCached(src string, lim Limits) (*Program, error) {
	return defaultCache.parse(src, lim)
}

func (c *parseCache) parse(src string, lim Limits) (*Program, error) {
	if len(src) > cacheMaxEntryBytes {
		return Parse(src, lim)
	}

	maxBytes, maxDepth := parseLimits(lim)
	key := cacheKey{sum: sha256.Sum256([]byte(src)), maxBytes: maxBytes, maxDepth: maxDepth}

	if e, ok := c.get(key); ok {
		return e.result()
	}

	prog, err := Parse(src, lim)
	e := &cacheEntry{key: key, prog: prog, cost: len(src) + cacheEntryOverhead}
	if err != nil {
		herr, ok := err.(*Error) //nolint:errorlint // Parse returns *Error unwrapped
		if !ok {
			return nil, err
		}
		e.prog = nil
		e.err = &Error{Kind: herr.Kind, Msg: herr.Msg}
		e.cost = len(herr.Msg) + cacheEntryOverhead
	}
	c.put(e)

	return prog, err
}

// result returns the entry's verdict as Parse would.
func (e *cacheEntry) result() (*Program, error) {
	if e.err != nil {
		return nil, &Error{Kind: e.err.Kind, Msg: e.err.Msg}
	}
	return e.prog, nil
}

func (c *parseCache) get(key cacheKey) (*cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	e, _ := el.Value.(*cacheEntry)
	return e, true
}

func (c *parseCache) put(e *cacheEntry) {
	if e.cost > c.maxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.entries[e.key]; ok {
		// a concurrent miss stored the same verdict first
		return
	}
	c.entries[e.key] = c.lru.PushFront(e)
	c.bytes += e.cost

	for c.bytes > c.maxBytes {
		el := c.lru.Back()
		old, _ := el.Value.(*cacheEntry)
		c.lru.Remove(el)
		delete(c.entries, old.key)
		c.bytes -= old.cost
	}
}

// stats returns the number of entries and their total cost.
func (c *parseCache) stats() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.bytes
}
