// Copyright © 2026 Luther Systems, Ltd. All right reserved.

package shape

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/luthersystems/svc/libhandlebars/internal/raymondref/lexer"
)

var fillerOnly = regexp.MustCompile(`[\p{L}\p{N}]`)

// CheckSkeleton verifies that skel carries no source text: every
// identifier is kept (a helper or keyword) or a hash name, every string
// literal is a renamed class value, and content and comments hold no
// letters but x and no digits but 0.
func CheckSkeleton(skel string) error {
	for _, tok := range lexer.Collect(skel) {
		switch tok.Kind {
		case lexer.TokenError:
			return fmt.Errorf("skeleton does not lex: %s", tok.Val)
		case lexer.TokenContent, lexer.TokenComment:
			if bad := fillerOnly.FindAllString(strings.NewReplacer("x", "", "0", "").Replace(tok.Val), 1); len(bad) > 0 {
				return fmt.Errorf("content at %d holds %q", tok.Pos, bad[0])
			}
		case lexer.TokenID:
			id := strings.TrimSuffix(strings.TrimPrefix(tok.Val, "["), "]")
			if !Kept(id) && !IdentPattern.MatchString(id) {
				return fmt.Errorf("identifier %q at %d is not a hash name", tok.Val, tok.Pos)
			}
		case lexer.TokenString:
			if !StringPattern.MatchString(tok.Val) {
				return fmt.Errorf("string %q at %d is not a renamed literal", tok.Val, tok.Pos)
			}
		default:
		}
	}
	return nil
}

var edgeVocabulary = func() map[string]bool {
	m := map[string]bool{}
	for _, l := range [][]string{edgeStrings, edgeNumbers, edgeDates, edgePhones} {
		for _, s := range l {
			m[s] = true
		}
	}
	return m
}()

// CheckContext verifies a generated context: every key is a hash name or
// a kept keyword, and every string value is from the generator's edge
// vocabulary or a renamed literal.
func CheckContext(ctx []byte) error {
	var v any
	if err := json.Unmarshal(ctx, &v); err != nil {
		return err
	}
	return checkValue(v)
}

func checkValue(v any) error {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if !Kept(k) && !IdentPattern.MatchString(k) {
				return fmt.Errorf("context key %q is not a hash name", k)
			}
			if err := checkValue(x); err != nil {
				return err
			}
		}
	case []any:
		for _, x := range v {
			if err := checkValue(x); err != nil {
				return err
			}
		}
	case string:
		if !edgeVocabulary[v] && !StringPattern.MatchString(v) {
			return fmt.Errorf("context string %q is not generated", v)
		}
	}
	return nil
}

// SourceTokens returns the words of length >= 4 in a source template that
// must not appear in its skeleton: identifiers, string literal contents
// and content words. Helper names and keywords are excluded, since a
// skeleton keeps them.
func SourceTokens(src string) []string {
	set := map[string]bool{}
	add := func(s string) {
		for _, w := range strings.FieldsFunc(s, wordSep) {
			if len([]rune(w)) >= 4 && !generic[strings.ToLower(w)] && !isFiller(w) {
				set[w] = true
			}
		}
	}
	for _, tok := range lexer.Collect(src) {
		switch tok.Kind {
		case lexer.TokenContent, lexer.TokenComment, lexer.TokenID, lexer.TokenString:
			add(tok.Val)
		default:
		}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// generic holds the words a skeleton or context may contain whatever the
// source: kept names, literal keywords and the generator's vocabulary.
var generic = func() map[string]bool {
	m := map[string]bool{"true": true, "false": true, "null": true}
	for k := range keep {
		m[strings.ToLower(k)] = true
	}
	for v := range edgeVocabulary {
		for _, w := range strings.FieldsFunc(v, wordSep) {
			m[strings.ToLower(w)] = true
		}
	}
	return m
}()

func isFiller(w string) bool {
	return strings.Trim(w, "x0") == ""
}

// Leaks returns the source tokens found in any output, compared as whole
// words without regard to case, so a token inside a hash name does not
// count.
func Leaks(tokens []string, outputs ...string) []string {
	words := map[string]bool{}
	for _, o := range outputs {
		for _, w := range strings.FieldsFunc(o, wordSep) {
			words[strings.ToLower(w)] = true
		}
	}
	var leaks []string
	for _, t := range tokens {
		if words[strings.ToLower(t)] {
			leaks = append(leaks, t)
		}
	}
	return leaks
}

func wordSep(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
}
