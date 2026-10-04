// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

// ParseForTest parses src with DefaultLimits().
func ParseForTest(src string) (*Program, error) {
	return Parse(src, DefaultLimits())
}

// NewParseCacheForTest returns ParseCachedMetered over a cache of its own,
// empty at first: its first parse of a template is a miss, later ones hit.
func NewParseCacheForTest() func(src string, lim Limits, m Meter) (*Program, error) {
	c := newParseCache(cacheMaxBytes)
	return func(src string, lim Limits, m Meter) (*Program, error) { return c.parse(src, lim, m) }
}
