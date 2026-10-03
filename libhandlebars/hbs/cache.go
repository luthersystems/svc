// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"sync"
)

// Parse cache bounds, in bytes of memory the entries retain. A program is
// weighed as its source length plus astBytesPerToken for each lexer token
// (the AST's size: plain text retains almost nothing beyond the source,
// tag-dense templates about 91 to 113 bytes per token, measured on amd64),
// plus cacheEntryOverhead; an error verdict as its message plus the
// overhead. A template over cacheMaxEntryBytes is never cached.
const (
	cacheMaxBytes      = 64 << 20
	cacheMaxEntryBytes = 1 << 20
	cacheEntryOverhead = 256
	astBytesPerToken   = 128
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
	prog   *Program
	err    *Error
	key    cacheKey
	cost   int
	charge int64 // what the miss charged, so a hit charges the same
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
// cached *Error. The cache retains about 64 MiB at most, weighing each
// program by its AST, and does not keep templates over 1 MiB.
//
// Callers that meter work must charge for a parse on a hit as on a miss,
// so the cache cannot change what a transaction costs.
func ParseCached(src string, lim Limits) (*Program, error) {
	return defaultCache.parse(src, lim, nil)
}

// ParseCachedMetered is ParseCached charging ParseCost to m on every call,
// cache hit or miss alike: a hit charges the token count the miss
// measured. A Meter error is returned unchanged.
func ParseCachedMetered(src string, lim Limits, m Meter) (*Program, error) {
	return defaultCache.parse(src, lim, m)
}

func (c *parseCache) parse(src string, lim Limits, m Meter) (*Program, error) {
	if len(src) > cacheMaxEntryBytes {
		p, _, err := parseMetered(src, lim, m)
		return p, err
	}

	maxBytes, maxDepth := parseLimits(lim)
	key := cacheKey{sum: sha256.Sum256([]byte(src)), maxBytes: maxBytes, maxDepth: maxDepth}

	if e, ok := c.get(key); ok {
		if err := charge(m, e.charge); err != nil {
			return nil, err
		}
		return e.result()
	}

	prog, cost, err := parseMetered(src, lim, m)
	var he *Error
	if err != nil && !errors.As(err, &he) {
		return nil, err // a Meter error: nothing to cache
	}
	e := &cacheEntry{key: key, prog: prog, charge: cost}
	if err == nil {
		e.cost = len(src) + astBytesPerToken*prog.tokens + cacheEntryOverhead
	} else {
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
