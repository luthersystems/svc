// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package hbs

// ParseForTest parses src with DefaultLimits().
func ParseForTest(src string) (*Program, error) {
	return Parse(src, DefaultLimits())
}
