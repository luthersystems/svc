package hbs

import "github.com/luthersystems/svc/libhandlebars/hbs/parser"

// ParseForTest parses with the raymond-copy parser until hbs.Parse exists.
// TEMPORARY (replace with Parse at integration).
func ParseForTest(src string) (*Program, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}
	return &Program{prog: prog}, nil
}
