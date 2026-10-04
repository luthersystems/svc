// Package lexer provides a handlebars tokenizer.
//
// It produces the token stream raymond's lexer produced, token for token
// (kinds, values, byte positions and line numbers), with two differences in
// how: it runs synchronously in the caller's goroutine, so an abandoned
// lexer leaks nothing, and it matches every pattern by hand in time linear
// in the bytes it examines, where raymond ran regular expressions against
// the whole remaining input at each content byte.
package lexer

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// References:
//   - https://github.com/wycats/handlebars.js/blob/master/src/handlebars.l
//   - https://github.com/golang/go/blob/master/src/text/template/parse/lex.go

const (
	// Mustaches detection
	escapedEscapedOpenMustache  = "\\\\{{"
	escapedOpenMustache         = "\\{{"
	openMustache                = "{{"
	closeMustache               = "}}"
	closeStripMustache          = "~}}"
	closeUnescapedStripMustache = "}~}}"
)

const eof = -1

// lexFunc represents a function that returns the next lexer function.
type lexFunc func(*Lexer) lexFunc

// commentKind selects the closing pattern of the comment being scanned.
type commentKind uint8

const (
	commentShort commentKind = iota // {{! ... }}: closes at \s*~?}}
	commentDash                     // {{!-- ... --}}: closes at \s*--~?}}
)

// Lexer is a lexical analyzer.
type Lexer struct {
	nextFunc lexFunc // the next function to execute
	input    string  // input to scan
	name     string  // lexer name, used for testing purpose
	pending  []Token // scanned tokens; pending[head:] not handed out yet
	head     int

	pos   int // current byte position in input string
	line  int // current line position in input string
	width int // size of last rune scanned from input string
	start int // start position of the token we are scanning

	// the shameful contextual properties needed because `nextFunc` is not enough
	closeComment commentKind // closing pattern of current comment
	rawBlock     bool        // are we parsing a raw block content ?
}

// characters not allowed in an identifier
const unallowedIDChars = " \n\t!\"#%&'()*+,./;<=>@[\\]^`{|}~"

// unallowedID[b] reports whether byte b ends an identifier. Every unallowed
// character is ASCII, so a byte test is exact: no byte of a multi-byte or
// invalid UTF-8 sequence is ASCII.
var unallowedID = func() [256]bool {
	var t [256]bool
	for i := range len(unallowedIDChars) {
		t[unallowedIDChars[i]] = true
	}
	return t
}()

// IsSpace reports whether b is in the regexp class \s, which is ASCII
// [\t\n\f\r ] in Go's RE2 syntax.
func IsSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

// spaceRun returns the end of the run of \s bytes starting at i.
func spaceRun(s string, i int) int {
	for i < len(s) && IsSpace(s[i]) {
		i++
	}
	return i
}

// Scan scans given input.
//
// Tokens can then be fetched sequentially thanks to NextToken() function on returned lexer.
func Scan(input string) *Lexer {
	return scanWithName(input, "")
}

// scanWithName scans given input, with a name used for testing
//
// Tokens can then be fetched sequentially thanks to NextToken() function on returned lexer.
func scanWithName(input string, name string) *Lexer {
	return &Lexer{
		input:    input,
		name:     name,
		line:     1,
		nextFunc: lexContent,
	}
}

// Collect scans and collect all tokens.
//
// This should be used for debugging purpose only. You should use Scan() and lexer.NextToken() functions instead.
func Collect(input string) []Token {
	var result []Token

	l := Scan(input)
	for {
		token := l.NextToken()
		result = append(result, token)

		if token.Kind == TokenEOF || token.Kind == TokenError {
			break
		}
	}

	return result
}

// NextToken returns the next scanned token. After the EOF or error token it
// returns EOF tokens (raymond's lexer blocked forever instead).
func (l *Lexer) NextToken() Token {
	for l.head == len(l.pending) {
		l.pending = l.pending[:0]
		l.head = 0
		if l.nextFunc == nil {
			return Token{Kind: TokenEOF, Pos: l.pos, Line: l.line}
		}
		l.nextFunc = l.nextFunc(l)
	}

	result := l.pending[l.head]
	l.head++

	return result
}

// next returns next character from input, or eof of there is nothing left to scan
func (l *Lexer) next() rune {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}

	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = w
	l.pos += l.width

	return r
}

func (l *Lexer) produce(kind TokenKind, val string) {
	l.pending = append(l.pending, Token{Kind: kind, Val: val, Pos: l.start, Line: l.line})

	// scanning a new token
	l.start = l.pos

	// update line number
	l.line += strings.Count(val, "\n")
}

// emit emits a new scanned token
func (l *Lexer) emit(kind TokenKind) {
	l.produce(kind, l.input[l.start:l.pos])
}

// emitContent emits scanned content
func (l *Lexer) emitContent() {
	if l.pos > l.start {
		l.emit(TokenContent)
	}
}

// emitString emits a scanned string
func (l *Lexer) emitString(delimiter rune) {
	str := l.input[l.start:l.pos]

	// replace escaped delimiters
	str = strings.ReplaceAll(str, "\\"+string(delimiter), string(delimiter))

	l.produce(TokenString, str)
}

// peek returns but does not consume the next character in the input
func (l *Lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// backup steps back one character
//
// WARNING: Can only be called once per call of next
func (l *Lexer) backup() {
	l.pos -= l.width
}

// ignoreskips all characters that have been scanned up to current position
func (l *Lexer) ignore() {
	l.start = l.pos
}

// accept scans the next character if it is included in given string
func (l *Lexer) accept(valid string) bool {
	if strings.ContainsRune(valid, l.next()) {
		return true
	}

	l.backup()

	return false
}

// acceptRun scans all following characters that are part of given string
func (l *Lexer) acceptRun(valid string) {
	for strings.ContainsRune(valid, l.next()) {
	}

	l.backup()
}

// errorf emits an error token
func (l *Lexer) errorf(format string, args ...any) lexFunc {
	l.pending = append(l.pending, Token{Kind: TokenError, Val: fmt.Sprintf(format, args...), Pos: l.start, Line: l.line})
	return nil
}

// isString returns true if content at current scanning position starts with given string
func (l *Lexer) isString(str string) bool {
	return strings.HasPrefix(l.input[l.pos:], str)
}

// at returns the byte at position i, or 0 past the end of input.
func (l *Lexer) at(i int) byte {
	if i < len(l.input) {
		return l.input[i]
	}
	return 0
}

//
// Hand-written matchers for raymond's regular expressions. Each anchored
// matcher returns the length of the match at the current position, or 0
// for no match (none of the patterns can match the empty string). Each
// examines only the bytes its match spans plus one, so no matcher rescans
// the rest of the input.
//

// openTilde returns the position after "{{" and an optional "~" at i, or -1.
func (l *Lexer) openTilde(i int) int {
	if !strings.HasPrefix(l.input[i:], openMustache) {
		return -1
	}
	i += 2
	if l.at(i) == '~' {
		i++
	}
	return i
}

// matchOpenChar matches ^\{\{~?<c>.
func (l *Lexer) matchOpenChar(c byte) int {
	i := l.openTilde(l.pos)
	if i < 0 || l.at(i) != c {
		return 0
	}
	return i + 1 - l.pos
}

// matchOpenCommentDash matches ^\{\{~?!--\s*.
func (l *Lexer) matchOpenCommentDash() int {
	i := l.openTilde(l.pos)
	if i < 0 || !strings.HasPrefix(l.input[i:], "!--") {
		return 0
	}
	return spaceRun(l.input, i+3) - l.pos
}

// matchCloseTilde matches ~?\}\} at i, returning the end or -1.
func (l *Lexer) matchCloseTilde(i int) int {
	if l.at(i) == '~' {
		i++
	}
	if strings.HasPrefix(l.input[i:], closeMustache) {
		return i + 2
	}
	return -1
}

// matchInverse matches ^(\{\{~?\^\s*~?\}\}|\{\{~?\s*else\s*~?\}\}).
func (l *Lexer) matchInverse() int {
	i := l.openTilde(l.pos)
	if i < 0 {
		return 0
	}
	if l.at(i) == '^' {
		if e := l.matchCloseTilde(spaceRun(l.input, i+1)); e >= 0 {
			return e - l.pos
		}
	}
	j := spaceRun(l.input, i)
	if strings.HasPrefix(l.input[j:], "else") {
		if e := l.matchCloseTilde(spaceRun(l.input, j+4)); e >= 0 {
			return e - l.pos
		}
	}
	return 0
}

// matchOpenInverseChain matches ^\{\{~?\s*else.
func (l *Lexer) matchOpenInverseChain() int {
	i := l.openTilde(l.pos)
	if i < 0 {
		return 0
	}
	j := spaceRun(l.input, i)
	if strings.HasPrefix(l.input[j:], "else") {
		return j + 4 - l.pos
	}
	return 0
}

// matchOpen matches ^\{\{~?&?.
func (l *Lexer) matchOpen() int {
	i := l.openTilde(l.pos)
	if i < 0 {
		return 0
	}
	if l.at(i) == '&' {
		i++
	}
	return i - l.pos
}

// matchCloseComment matches the closing pattern of the current comment at
// i, ^\s*~?\}\} or ^\s*--~?\}\}, returning the end of the match or -1.
func (l *Lexer) matchCloseComment(i int) int {
	j := spaceRun(l.input, i)
	if l.closeComment == commentDash {
		if !strings.HasPrefix(l.input[j:], "--") {
			return -1
		}
		j += 2
	}
	return l.matchCloseTilde(j)
}

// isLookahead reports whether b is in [\s=~}/)|].
func isLookahead(b byte) bool {
	return IsSpace(b) || strings.IndexByte("=~}/)|", b) >= 0
}

// isLiteralLookahead reports whether b is in [\s~})].
func isLiteralLookahead(b byte) bool {
	return IsSpace(b) || strings.IndexByte("~})", b) >= 0
}

// matchLiteral matches ^<word>[\s~})].
func (l *Lexer) matchLiteral(word string) bool {
	n := l.pos + len(word)
	return l.isString(word) && n < len(l.input) && isLiteralLookahead(l.input[n])
}

// lexContent scans content (ie: not between mustaches)
func lexContent(l *Lexer) lexFunc {
	if l.rawBlock {
		// {{{{/
		i := strings.Index(l.input[l.pos:], "{{{{/")
		if i == -1 {
			return l.errorf("Unclosed raw block")
		}
		l.rawBlock = false
		l.pos += i

		l.emitContent()
		return lexOpenMustache
	}

	// raymond tried every pattern below at every rune. Each starts with
	// '\' or '{', both ASCII, so skip to the next such byte. Rune
	// boundaries are preserved: an ASCII byte never occurs inside a UTF-8
	// sequence, valid or not.
	for {
		i := strings.IndexAny(l.input[l.pos:], "\\{")
		if i < 0 {
			l.pos = len(l.input)

			// emit scanned content
			l.emitContent()

			// this is over
			l.emit(TokenEOF)
			return nil
		}
		l.pos += i

		var next lexFunc

		switch {
		case l.isString(escapedEscapedOpenMustache):
			// \\{{

			// emit content with only one escaped escape
			l.next()
			l.emitContent()

			// ignore second escaped escape
			l.next()
			l.ignore()

			// hand out the content token, then continue scanning
			return lexContent
		case l.isString(escapedOpenMustache):
			// \{{
			next = lexEscapedOpenMustache
		case l.matchOpenCommentDash() != 0:
			// {{!--
			l.closeComment = commentDash

			next = lexComment
		case l.matchOpenChar('!') != 0:
			// {{!
			l.closeComment = commentShort

			next = lexComment
		case l.isString(openMustache):
			// {{
			next = lexOpenMustache
		}

		if next != nil {
			// emit scanned content
			l.emitContent()

			// scan next token
			return next
		}

		// scan next rune ('\' or '{', one byte)
		l.pos++
	}
}

// lexEscapedOpenMustache scans \{{
func lexEscapedOpenMustache(l *Lexer) lexFunc {
	// ignore escape character
	l.next()
	l.ignore()

	// scan mustaches
	for l.peek() == '{' {
		l.next()
	}

	return lexContent
}

// lexOpenMustache scans {{
func lexOpenMustache(l *Lexer) lexFunc {
	var n int
	var tok TokenKind

	nextFunc := lexExpression

	switch {
	case l.isString("{{{{/"):
		n, tok = 5, TokenOpenEndRawBlock
	case l.isString("{{{{"):
		n, tok = 4, TokenOpenRawBlock
		l.rawBlock = true
	case l.matchOpenChar('{') != 0:
		n, tok = l.matchOpenChar('{'), TokenOpenUnescaped
	case l.matchOpenChar('#') != 0:
		n, tok = l.matchOpenChar('#'), TokenOpenBlock
	case l.matchOpenChar('/') != 0:
		n, tok = l.matchOpenChar('/'), TokenOpenEndBlock
	case l.matchOpenChar('>') != 0:
		n, tok = l.matchOpenChar('>'), TokenOpenPartial
	case l.matchInverse() != 0:
		n, tok = l.matchInverse(), TokenInverse
		nextFunc = lexContent
	case l.matchOpenChar('^') != 0:
		n, tok = l.matchOpenChar('^'), TokenOpenInverse
	case l.matchOpenInverseChain() != 0:
		n, tok = l.matchOpenInverseChain(), TokenOpenInverseChain
	case l.matchOpen() != 0:
		n, tok = l.matchOpen(), TokenOpen
	default:
		// this is rotten
		panic("Current pos MUST be an opening mustache")
	}

	l.pos += n
	l.emit(tok)

	return nextFunc
}

// lexCloseMustache scans }} or ~}}
func lexCloseMustache(l *Lexer) lexFunc {
	var n int
	var tok TokenKind

	switch {
	case l.isString("}}}}"):
		n, tok = 4, TokenCloseRawBlock
	case l.isString("}}}"):
		n, tok = 3, TokenCloseUnescaped
	case l.isString("}~}}"):
		n, tok = 4, TokenCloseUnescaped
	case l.isString(closeMustache):
		n, tok = 2, TokenClose
	case l.isString(closeStripMustache):
		n, tok = 3, TokenClose
	default:
		// this is rotten
		panic("Current pos MUST be a closing mustache")
	}

	l.pos += n
	l.emit(tok)

	return lexContent
}

// lexExpression scans inside mustaches
func lexExpression(l *Lexer) lexFunc {
	// search close mustache delimiter
	if l.isString(closeMustache) || l.isString(closeStripMustache) || l.isString(closeUnescapedStripMustache) {
		return lexCloseMustache
	}

	// search some patterns before advancing scanning position

	// "as |": ^as\s+\|
	if l.isString("as") {
		if j := spaceRun(l.input, l.pos+2); j > l.pos+2 && l.at(j) == '|' {
			l.pos = j + 1
			l.emit(TokenOpenBlockParams)
			return lexExpression
		}
	}

	// ..
	if l.isString("..") {
		l.pos += len("..")
		l.emit(TokenID)
		return lexExpression
	}

	// .: ^\.[\s=~}/)|]
	if l.at(l.pos) == '.' && l.pos+1 < len(l.input) && isLookahead(l.input[l.pos+1]) {
		l.pos += len(".")
		l.emit(TokenID)
		return lexExpression
	}

	// true
	if l.matchLiteral("true") {
		l.pos += len("true")
		l.emit(TokenBoolean)
		return lexExpression
	}

	// false
	if l.matchLiteral("false") {
		l.pos += len("false")
		l.emit(TokenBoolean)
		return lexExpression
	}

	// let's scan next character
	switch r := l.next(); {
	case r == eof:
		return l.errorf("Unclosed expression")
	case isIgnorable(r):
		return lexIgnorable
	case r == '(':
		l.emit(TokenOpenSexpr)
	case r == ')':
		l.emit(TokenCloseSexpr)
	case r == '=':
		l.emit(TokenEquals)
	case r == '@':
		l.emit(TokenData)
	case r == '"' || r == '\'':
		l.backup()
		return lexString
	case r == '/' || r == '.':
		l.emit(TokenSep)
	case r == '|':
		l.emit(TokenCloseBlockParams)
	case r == '+' || r == '-' || (r >= '0' && r <= '9'):
		l.backup()
		return lexNumber
	case r == '[':
		return lexPathLiteral
	case !strings.ContainsRune(unallowedIDChars, r):
		l.backup()
		return lexIdentifier
	default:
		return l.errorf("Unexpected character in expression: '%c'", r)
	}

	return lexExpression
}

// lexComment scans {{!-- or {{!
//
// Like raymond, it tries the closing pattern at every rune from the opening
// "{{" on. A closing pattern starts with a run of \s, and every position
// inside one run reaches the same end of run, so a run that fails is
// skipped whole: the scan is linear where raymond's was quadratic in the
// length of a whitespace run.
func lexComment(l *Lexer) lexFunc {
	i := l.pos
	for {
		if e := l.matchCloseComment(i); e >= 0 {
			l.pos = e
			l.emit(TokenComment)

			return lexContent
		}
		if i >= len(l.input) {
			l.pos = i
			return l.errorf("Unclosed comment")
		}
		if IsSpace(l.input[i]) {
			i = spaceRun(l.input, i)
			continue
		}
		_, w := utf8.DecodeRuneInString(l.input[i:])
		i += w
	}
}

// lexIgnorable scans all following ignorable characters
func lexIgnorable(l *Lexer) lexFunc {
	for isIgnorable(l.peek()) {
		l.next()
	}
	l.ignore()

	return lexExpression
}

// lexString scans a string
func lexString(l *Lexer) lexFunc {
	// get string delimiter
	delim := l.next()
	var prev rune

	// ignore delimiter
	l.ignore()

	for {
		r := l.next()
		if r == eof || r == '\n' {
			return l.errorf("Unterminated string")
		}

		if (r == delim) && (prev != '\\') {
			break
		}

		prev = r
	}

	// remove end delimiter
	l.backup()

	// emit string
	l.emitString(delim)

	// skip end delimiter
	l.next()
	l.ignore()

	return lexExpression
}

// lexNumber scans a number: decimal, octal, hex, float, or imaginary. This
// isn't a perfect number scanner - for instance it accepts "." and "0x0.2"
// and "089" - but when it's wrong the input is invalid and the parser (via
// strconv) will notice.
//
// NOTE: borrowed from https://github.com/golang/go/tree/master/src/text/template/parse/lex.go
func lexNumber(l *Lexer) lexFunc {
	if !l.scanNumber() {
		return l.errorf("bad number syntax: %q", l.input[l.start:l.pos])
	}
	if sign := l.peek(); sign == '+' || sign == '-' {
		// Complex: 1+2i. No spaces, must end in 'i'.
		if !l.scanNumber() || l.input[l.pos-1] != 'i' {
			return l.errorf("bad number syntax: %q", l.input[l.start:l.pos])
		}
	}
	l.emit(TokenNumber)
	return lexExpression
}

// scanNumber scans a number
//
// NOTE: borrowed from https://github.com/golang/go/tree/master/src/text/template/parse/lex.go
func (l *Lexer) scanNumber() bool {
	// Optional leading sign.
	l.accept("+-")

	// Is it hex?
	digits := "0123456789"

	if l.accept("0") && l.accept("xX") {
		digits = "0123456789abcdefABCDEF"
	}

	l.acceptRun(digits)

	if l.accept(".") {
		l.acceptRun(digits)
	}

	if l.accept("eE") {
		l.accept("+-")
		l.acceptRun("0123456789")
	}

	// Is it imaginary?
	l.accept("i")

	// Next thing mustn't be alphanumeric.
	if isAlphaNumeric(l.peek()) {
		l.next()
		return false
	}

	return true
}

// lexIdentifier scans an ID: ^[^unallowedIDChars]+
func lexIdentifier(l *Lexer) lexFunc {
	i := l.pos
	for i < len(l.input) && !unallowedID[l.input[i]] {
		i++
	}
	if i == l.pos {
		// this is rotten
		panic("Identifier expected")
	}

	l.pos = i
	l.emit(TokenID)

	return lexExpression
}

// lexPathLiteral scans an [ID]
func lexPathLiteral(l *Lexer) lexFunc {
	for {
		r := l.next()
		if r == eof || r == '\n' {
			return l.errorf("Unterminated path literal")
		}

		if r == ']' {
			break
		}
	}

	l.emit(TokenID)

	return lexExpression
}

// isIgnorable returns true if given character is ignorable (ie. whitespace of line feed)
func isIgnorable(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// isAlphaNumeric reports whether r is an alphabetic, digit, or underscore.
//
// NOTE borrowed from https://github.com/golang/go/tree/master/src/text/template/parse/lex.go
func isAlphaNumeric(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
