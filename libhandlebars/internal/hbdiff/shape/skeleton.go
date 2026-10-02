// Package shape turns a private handlebars template into a skeleton that
// keeps everything the engine's behaviour depends on (block structure,
// else chains, ../ depth, triple vs double stash, ~ and standalone
// whitespace, helper names and helper-significant literals) and nothing
// that identifies the source: content text becomes filler, identifiers
// and string literals become salted hashes. It also infers a context
// schema from a skeleton and generates edge-class contexts for it.
package shape

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	raymond "github.com/luthersystems/svc/libhandlebars/internal/raymondref"
	"github.com/luthersystems/svc/libhandlebars/internal/raymondref/lexer"
)

// Helpers are the helper names svc registered, plus raymond's built-in
// block helpers and the unregistered names raymond reserves.
var Helpers = []string{
	"eq", "len", "not", "and", "or", "gt", "gte", "lt", "lte", "times", "div", "mod",
	"date-diff-month", "is-after", "date-add-months", "to-int", "plus", "minus",
	"select", "global", "round-to-nth", "in-string-array", "prettyp-num-en",
	"possessive", "date-beautify", "date-DDMMYY-slash", "date-DDMMYYYY-slash",
	"date-DDMMYYYY", "format-phone-gb", "escape-uri-component", "to-str",
	"if", "unless", "each", "with", "lookup", "log",
}

// Keywords are identifiers kept verbatim besides Helpers: path keywords,
// data variables raymond defines, and hash keys whose name a helper reads.
var Keywords = []string{
	"this", ".", "..",
	"index", "key", "first", "last", "root",
	"from", "where", "key", "val", "haystack", "needle", "includeZero",
}

var keep = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range Helpers {
		m[s] = true
	}
	for _, s := range Keywords {
		m[s] = true
	}
	return m
}()

// Kept reports whether id is left unrenamed in skeletons.
func Kept(id string) bool { return keep[id] }

// Patterns every renamed token in a skeleton matches.
var (
	IdentPattern  = regexp.MustCompile(`^f[0-9a-f]{6,}$`)
	StringPattern = regexp.MustCompile(`^(s[0-9a-f]{6,}|[0-9.eE+-]*|\d{4}-\d{2}-\d{2}|f[0-9a-f]{6,}=(s[0-9a-f]{6,}|[0-9.eE+-]*|\d{4}-\d{2}-\d{2}))$`)
	datePattern   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Anonymizer renames identifiers and literals consistently across the
// templates of one run. Names are HMAC-SHA256 of the original under a
// salt that is never committed, so a skeleton cannot be reversed by
// hashing guessed names.
type Anonymizer struct {
	salt    []byte
	idents  map[string]string
	strs    map[string]string
	used    map[string]string // output -> input, to detect collisions
	Strings []string          // every renamed string literal value, for context pools
}

// NewAnonymizer returns an Anonymizer keyed by salt.
func NewAnonymizer(salt []byte) *Anonymizer {
	return &Anonymizer{salt: salt, idents: map[string]string{}, strs: map[string]string{}, used: map[string]string{}}
}

func (a *Anonymizer) mac(kind, s string) string {
	h := hmac.New(sha256.New, a.salt)
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func (a *Anonymizer) unique(prefix, kind, s string) string {
	full := a.mac(kind, s)
	for n := 6; n <= len(full); n++ {
		out := prefix + full[:n]
		if prev, ok := a.used[out]; !ok || prev == kind+"\x00"+s {
			a.used[out] = kind + "\x00" + s
			return out
		}
	}
	panic("shape: hash collision on a full HMAC")
}

// Ident renames one path segment, hash key or block parameter.
func (a *Anonymizer) Ident(id string) string {
	if keep[id] {
		return id
	}
	if r, ok := a.idents[id]; ok {
		return r
	}
	r := a.unique("f", "id", id)
	a.idents[id] = r
	return r
}

// String renames a string literal, keeping its class: empty stays empty,
// a YYYY-MM-DD date stays a valid date, a number stays a number of the
// same form, anything else becomes s<hash>.
func (a *Anonymizer) String(s string) string {
	if r, ok := a.strs[s]; ok {
		return r
	}
	var r string
	switch {
	case s == "":
		r = ""
	case datePattern.MatchString(s):
		h := a.mac("date", s)
		v, _ := strconv.ParseUint(h[:8], 16, 64)
		r = fmt.Sprintf("%04d-%02d-%02d", 2000+v%30, 1+(v/30)%12, 1+(v/360)%28)
	case isNumeric(s):
		r = a.Number(s)
	default:
		r = a.unique("s", "str", s)
	}
	a.strs[s] = r
	a.Strings = append(a.Strings, r)
	return r
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil && strings.Trim(s, "0123456789.eE+-") == ""
}

// Number keeps short numbers (they are counts, digits and flags) and
// replaces the digits of longer ones, keeping sign, point and exponent.
func (a *Anonymizer) Number(s string) string {
	if len(s) <= 3 {
		return s
	}
	h := a.mac("num", s)
	var b strings.Builder
	j := 0
	for i, c := range s {
		if c >= '0' && c <= '9' {
			d := '0' + h[j%len(h)]%10
			if i == 0 || (i == 1 && (s[0] == '-' || s[0] == '+')) {
				d = '1' + h[j%len(h)]%9
			}
			b.WriteByte(d)
			j++
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// Filler replaces content text. Each line keeps its leading and trailing
// whitespace (standalone and ~ rules depend on it); the text between
// becomes a run of x whose length is the original rounded up to a multiple
// of 16, so neither words nor their lengths survive. A line holding a
// brace or backslash keeps every other character and only maps letters to
// x and digits to 0, since those characters can change how raymond lexes
// the mustaches next to them.
func Filler(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		body := strings.TrimRight(line, " \t\r")
		trail := line[len(body):]
		inner := strings.TrimLeft(body, " \t")
		b.WriteString(body[:len(body)-len(inner)])
		switch {
		case inner == "":
		case strings.ContainsAny(inner, "{}\\"):
			for _, c := range inner {
				switch {
				case unicode.IsLetter(c):
					b.WriteByte('x')
				case unicode.IsDigit(c):
					b.WriteByte('0')
				default:
					b.WriteRune(c)
				}
			}
		default:
			b.WriteString(strings.Repeat("x", (len(inner)+15)/16*16))
		}
		b.WriteString(trail)
	}
	return b.String()
}

// Skeleton rewrites src. The template must parse; the rewrite works on
// raymond's token stream so every byte between tokens (whitespace inside
// mustaches, quotes, ~) is copied unchanged.
func (a *Anonymizer) Skeleton(src string) (string, error) {
	if _, err := raymond.Parse(src); err != nil {
		return "", fmt.Errorf("template does not parse: %w", err)
	}
	toks := lexer.Collect(src)
	var b strings.Builder
	cursor := 0
	for i, tok := range toks {
		switch tok.Kind {
		case lexer.TokenEOF:
			b.WriteString(src[cursor:])
			return b.String(), nil
		case lexer.TokenError:
			return "", fmt.Errorf("lexer error: %s", tok.Val)
		default:
		}
		if tok.Pos < cursor {
			return "", fmt.Errorf("token %s at %d overlaps %d", tok, tok.Pos, cursor)
		}
		b.WriteString(src[cursor:tok.Pos])
		end := tok.Pos + len(tok.Val)
		var repl string
		switch tok.Kind {
		case lexer.TokenContent, lexer.TokenComment:
			repl = Filler(tok.Val)
		case lexer.TokenID:
			repl = a.id(tok.Val)
		case lexer.TokenString:
			end = stringEnd(src, tok.Pos)
			repl = a.stringLit(tok.Val, toks, i)
		case lexer.TokenNumber:
			repl = a.Number(tok.Val)
		default:
			repl = tok.Val
		}
		if end > len(src) || (tok.Kind != lexer.TokenString && src[tok.Pos:end] != tok.Val) {
			return "", fmt.Errorf("token %s does not match the source at %d", tok, tok.Pos)
		}
		b.WriteString(repl)
		cursor = end
	}
	return "", errors.New("token stream ended without EOF")
}

// stringEnd finds the closing delimiter of a string token whose content
// starts at pos, with the lexer's rule (a delimiter not preceded by \).
func stringEnd(src string, pos int) int {
	delim := src[pos-1]
	var prev byte
	for i := pos; i < len(src); i++ {
		if src[i] == delim && prev != '\\' {
			return i
		}
		prev = src[i]
	}
	return len(src)
}

func (a *Anonymizer) id(val string) string {
	if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
		return "[" + a.Ident(val[1:len(val)-1]) + "]"
	}
	return a.Ident(val)
}

// stringLit renames a string literal. A where="k=v" argument renames k as
// an identifier (it names a field) and v as a string.
func (a *Anonymizer) stringLit(val string, toks []lexer.Token, i int) string {
	if i >= 2 && toks[i-1].Kind == lexer.TokenEquals && toks[i-2].Kind == lexer.TokenID && toks[i-2].Val == "where" {
		if k, v, ok := strings.Cut(val, "="); ok && !strings.Contains(v, "=") {
			return a.Ident(k) + "=" + a.String(v)
		}
	}
	return a.String(val)
}
